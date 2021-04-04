package main

import (
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"time"

	"github.com/pelletier/go-toml"
)

var config struct {
	NumFetcher   int    `toml:"numFetcher"`
	NumParser    int    `toml:"numParser"`
	NumRenderer  int    `toml:"numRenderer"`
	TemplateName string `toml:"templateName"`
	RetryPeriod  int    `toml:"retryPeriod"`
}

var version = "debug"

var outputTemplate *template.Template

type logWriter struct {
}

func (writer logWriter) Write(bytes []byte) (int, error) {
	return fmt.Print(time.Now().UTC().Format("2006-01-02 15:04:05 ") + string(bytes))
}

func init() {
	// setup log time format
	// https://stackoverflow.com/a/36140590/6091246
	log.SetFlags(0)
	log.SetOutput(new(logWriter))

	dataStr, _ := ioutil.ReadFile("config.toml")
	err := toml.Unmarshal(dataStr, &config)
	if err != nil {
		log.Fatal(err)
	}

	outputPath := "output"
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		err = os.Mkdir(outputPath, 0755)
		if err != nil {
			log.Fatalf("Error creating output folder: %v", err)
		}
	}

	fmt.Fprintf(os.Stderr, "templateName: %s", config.TemplateName)

	rand.Seed(time.Now().UnixNano())

	// outputTemplate is used to render output
	outputTemplate = template.Must(template.New(config.TemplateName).Funcs(
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
		}).ParseFiles("template/" + config.TemplateName))

}

func main() {
	println("tiebaSpider")
	println("version:", version)
	println("project url: https://github.com/hjhee/tiebaSpider")

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
	parseSelect:
		select {
		case <-done:
			break parseSelect
		case err, ok := <-errcFetch:
			if !ok {
				errcFetch = nil
				log.Printf("[Fetch] job done")
				continue
			}
			fmt.Fprintf(os.Stderr, "[Fetch] error: %v\n", err)
		case err, ok := <-errcParse:
			if !ok {
				errcParse = nil
				log.Printf("[Parse] job done")
				continue
			}
			fmt.Fprintf(os.Stderr, "[Parse] error: %v\n", err)
		case err, ok := <-errcRender:
			if !ok {
				errcRender = nil
				log.Printf("[Template] job done")
				continue
			}
			fmt.Fprintf(os.Stderr, "[Template] error: %v\n", err)
		case file, ok := <-outputc:
			if ok {
				log.Printf("[Template] %s done\n", file)
			}
		}
	}
}
