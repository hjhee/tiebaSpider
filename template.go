package main

import (
	"html/template"
	"io"
	"log"
	"sort"
	"sync"
)

func generateHTML(w io.Writer, tmpl *template.Template, c *TemplateField) {
	var wg sync.WaitGroup
	var posts = make([]*OutputField, 0, 100)
	var lzls = make(map[uint64]*LzlComment)
	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("starting HTML generation")
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := false
			for !done {
				select {
				case r, ok := <-c.Posts:
					if !ok {
						c.Posts = nil
					} else {
						posts = append(posts, r)
					}
				case r, ok := <-c.Lzls:
					if !ok {
						c.Lzls = nil
					} else {
						for k, v := range r {
							lzls[k] = v
						}
					}
				}
				if c.Posts == nil && c.Lzls == nil {
					done = true
				}
			}
			sort.Slice(posts, func(a, b int) bool {
				return posts[a].PostNO < posts[b].PostNO
			})
		}()
		// go func() {
		// 	wg.Wait()
		// 	log.Printf("ending HTML generation")
		// 	tmpl.Execute(o, posts)
		// }()
	}()
	wg.Wait()
	log.Printf("ending HTML generation")
	// log.Printf("posts:\n")
	// for i := range posts {
	// 	log.Printf("{PostNO:%d,\nUserName:%s,\nContent:%s\n}", posts[i].PostNo, posts[i].UserName, posts[i].Content)
	// }
	if err := tmpl.Execute(w, struct {
		Title string
		Posts []*OutputField
		Lzls  map[uint64]*LzlComment
	}{Title: "贴吧", Posts: posts, Lzls: lzls}); err != nil {
		log.Printf("error executing template: %v", err)
	}
	// tmpl.Execute(w, posts)
}
