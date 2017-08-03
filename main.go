package main

import (
	"html/template"
	"log"
	"math/rand"
	"time"
)

const (
	numFetcher  = 100
	numParser   = 50
	numRenderer = 5
)

var outputTemplate *template.Template

func init() {
	rand.Seed(time.Now().UnixNano())
	outputTemplate = template.Must(template.New("template1.html").Funcs(
		template.FuncMap{"convertTime": func(ts int64) string {
			// Time return formatted time
			return time.Unix(ts, 0).In(time.Local).Format("2006-01-02 15:04")
		},
		}).ParseFiles("template/template1.html"))
}

func main() {
	done := make(chan struct{})
	defer close(done)

	pc, errcFetch := fetchHTMLList(done, "url.txt")
	tempc, errcParse := parseHTML(done, pc)
	outputc, errcRender := renderHTML(done, tempc, outputTemplate)

	for {
		if errcFetch == nil && errcParse == nil && errcRender == nil {
			log.Printf("Job done!\n")
			break
		}
		select {
		case err, ok := <-errcFetch:
			if !ok {
				errcFetch = nil
				log.Printf("[Fetch] job done")
				continue
			}
			log.Fatalf("[Fetch] %v\n", err)
		case err, ok := <-errcParse:
			if !ok {
				errcParse = nil
				log.Printf("[Parse] job done")
				continue
			}
			log.Fatalf("[Parse] %v\n", err)
		case err, ok := <-errcRender:
			if !ok {
				errcRender = nil
				log.Printf("[Template] job done")
				continue
			}
			log.Printf("[Template] error: %v\n", err)
		case file := <-outputc:
			log.Printf("[Template] %s done\n", file)
		}
	}
}
