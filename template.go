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

func renderHTML(done <-chan struct{}, pc *PageChannel, tempc <-chan *TemplateField, tmpl *template.Template) (chan string, chan error) {
	outputc := make(chan string)
	errc := make(chan error)

	// spawn renderers
	var wg sync.WaitGroup
	wg.Add(config.NumRenderer)
	for i := 0; i < config.NumRenderer; i++ {
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

					if ret, err := postProcessing(done, pc, t); err != nil {
						errc <- err
						continue
					} else if ret {
						t.mutex.Lock()
						if t.send {
							t.send = false
						}
						t.mutex.Unlock()
						continue
					}

					sort.Slice(t.Comments, func(a, b int) bool {
						return t.Comments[a].PostNO < t.Comments[b].PostNO
					})

					for _, v := range t.Lzls.Map {
						sort.Slice(v.Info, func(a, b int) bool {
							return v.Info[a].Index < v.Info[b].Index
						})
					}

					// no longer merge as requested in issue #8
					// t.Merge()
					// t.Unique()

					// log.Printf("writing file output/file_%s.json", t.FileName())
					filename := fmt.Sprintf("output/file_%s.json", t.FileName())

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

					filename = fmt.Sprintf("output/file_%s.html", t.FileName())
					err = writeOutput(filename, func(w *bufio.Writer) error {
						if err := tmpl.Execute(w, struct {
							Title    string
							Url      string
							Comments []*OutputField
							Lzls     map[uint64]*LzlComment
						}{Title: t.Title, Url: t.Url, Comments: t.Comments, Lzls: t.Lzls.Map}); err != nil {
							return fmt.Errorf("error executing template %s: %v", filename, err)
						}
						return nil
					})

					if err != nil {
						errc <- err
						continue
					}

					outputc <- filename // report finished task
					t.SetRendered(true)

					if config.StoreExternalResource {
						pc.Del(1)
					}
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
