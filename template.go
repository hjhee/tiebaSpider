package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"regexp"
	"sort"
	"sync"
)

func writeOutput(filename string, callback func(w *bufio.Writer) error) error {
	f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("error creating output file %s: %v", filename, err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	err = callback(w)
	if err != nil {
		return fmt.Errorf("error writing to bufio %s, %v", filename, err)
	}
	err = w.Flush()
	if err != nil {
		return err
	}
	return nil
}

func renderHTML(done <-chan struct{}, tempc <-chan *TemplateField, tmpl *template.Template) (chan string, chan error) {
	outputc := make(chan string)
	errc := make(chan error)

	// spawn renderers
	var wg sync.WaitGroup
	wg.Add(numRenderer)
	for i := 0; i < numRenderer; i++ {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return

				case t, ok := <-tempc:
					if !ok {
						return // no new task from parser, exit
					}

					sort.Slice(t.Comments, func(a, b int) bool {
						return t.Comments[a].PostNO < t.Comments[b].PostNO
					})

					for _, v := range t.Lzls.Map {
						sort.Slice(v.Info, func(a, b int) bool {
							return v.Info[a].Index < v.Info[b].Index
						})
					}

					t.Merge()
					// t.Unique()

					// #6: remove illegal character in title
					// ref: https://www.codeproject.com/tips/758861/removing-characters-which-are-not-allowed-in-windo
					filenameRegex := regexp.MustCompile(`[\\/:*?""<>|]`)
					validFilename := filenameRegex.ReplaceAllLiteralString(t.Title, "")

					filename := fmt.Sprintf("output/file_%s.json", validFilename)

					b, err := json.Marshal(t)
					if err != nil {
						errc <- err
						continue
					}

					err = writeOutput(filename, func(w *bufio.Writer) error {
						_, err := w.Write(b)
						return err
					})

					if err != nil {
						errc <- err
						continue
					}

					filename = fmt.Sprintf("output/file_%s.html", validFilename)
					err = writeOutput(filename, func(w *bufio.Writer) error {
						if err := tmpl.Execute(w, struct {
							Title    string
							Comments []*OutputField
							Lzls     map[uint64]*LzlComment
						}{Title: t.Title, Comments: t.Comments, Lzls: t.Lzls.Map}); err != nil {
							return fmt.Errorf("error executing template %s: %v", filename, err)
						}
						return nil
					})

					if err != nil {
						errc <- err
						continue
					}

					outputc <- filename // report finished task
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(errc)
		close(outputc)
	}()
	return outputc, errc
}
