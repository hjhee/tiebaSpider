package main

import (
	"fmt"
	"html/template"
	"os"
	"sort"
	"sync"
)

func renderHTML(done <-chan struct{}, tempc <-chan *TemplateField, tmpl *template.Template) (chan string, chan error) {
	outputc := make(chan string)
	errc := make(chan error)

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
						return
					}
					filename := fmt.Sprintf("output/file_%s.html", t.Title)
					err := func(filename string) error {
						sort.Slice(t.Posts, func(a, b int) bool {
							return t.Posts[a].PostNO < t.Posts[b].PostNO
						})
						f, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
						if err != nil {
							return fmt.Errorf("error creating output file output/file_%s.html: %v", t.Title, err)
						}
						defer f.Close()
						if err := tmpl.Execute(f, struct {
							Title string
							Posts []*OutputField
							Lzls  map[uint64]*LzlComment
						}{Title: t.Title, Posts: t.Posts, Lzls: t.Lzls.Map}); err != nil {
							return fmt.Errorf("error executing template: %v", err)
						}
						return nil
					}(filename)

					if err != nil {
						errc <- err
						continue
					}

					outputc <- filename
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
