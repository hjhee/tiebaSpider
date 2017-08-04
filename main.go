package main

import (
	"html/template"
	"log"
	"math/rand"
	"os"
	"time"
)

const (
	numFetcher  = 100
	numParser   = 50
	numRenderer = 5

	templateName = "template1.html"
)

var outputTemplate *template.Template

func init() {
	outputPath := "./output"
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		err = os.Mkdir(outputPath, 0644)
		if err != nil {
			log.Fatalf("Error creating output folder: %v", err)
		}
	}
	rand.Seed(time.Now().UnixNano())
	// outputTemplate is used to render output
	outputTemplate = template.Must(template.New(templateName).Funcs(
		template.FuncMap{"convertTime": func(ts int64) string {
			// convertTime converts unix timestamp to the following format
			// How do I format an unix timestamp to RFC3339 - golang?
			// https://stackoverflow.com/a/21814954/6091246
			// Convert UTC to “local” time - Go
			// https://stackoverflow.com/a/45137855/6091246
			// Using Functions Inside Go Templates
			// https://www.calhoun.io/using-functions-inside-go-templates/
			// Go template function
			// https://stackoverflow.com/a/20872724/6091246
			return time.Unix(ts, 0).In(time.Local).Format("2006-01-02 15:04")
		},
		}).ParseFiles("template/" + templateName))
}

func main() {
	// closing done to force all goroutines to quit
	// Go Concurrency Patterns: Pipelines and cancellation
	// https://blog.golang.org/pipelines
	done := make(chan struct{})
	defer close(done)

	pc, errcFetch := fetchHTMLList(done, "url.txt")
	tempc, errcParse := parseHTML(done, pc)
	outputc, errcRender := renderHTML(done, tempc, outputTemplate)

	for {
		// programme exits when all error channels are closed:
		// breaking out of a select statement when all channels are closed
		// https://stackoverflow.com/a/13666733/6091246
		if errcFetch == nil && errcParse == nil && errcRender == nil {
			log.Printf("Job done!\n")
			break
		}
		select {
		case <-done:
			break
		case err, ok := <-errcFetch:
			if !ok {
				errcFetch = nil
				log.Printf("[Fetch] job done")
				continue
			}
			log.Printf("[Fetch] error: %v\n", err)
		case err, ok := <-errcParse:
			if !ok {
				errcParse = nil
				log.Printf("[Parse] job done")
				continue
			}
			log.Printf("[Parse] error: %v\n", err)
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
