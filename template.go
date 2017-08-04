package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
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

					sort.Slice(t.Posts, func(a, b int) bool {
						return t.Posts[a].PostNO < t.Posts[b].PostNO
					})

					t.Merge()
					// t.Unique()

					filename := fmt.Sprintf("output/file_%s.json", t.Title)

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

					filename = fmt.Sprintf("output/file_%s.html", t.Title)
					err = writeOutput(filename, func(w *bufio.Writer) error {

						if err := tmpl.Execute(w, struct {
							Title string
							Posts []*OutputField
							Lzls  map[uint64]*LzlComment
						}{Title: t.Title, Posts: t.Posts, Lzls: t.Lzls.Map}); err != nil {
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
