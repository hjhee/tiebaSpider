package main

import (
	"bufio"
	"fmt"
	"strings"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"
	"time"
)

// http://tieba.baidu.com/p/totalComment?t=1501582373&tid=3922635509&fid=867983&pn=2&see_lz=0

func fetchHTMLList(done <-chan struct{}, filename string) (*PageChannel, <-chan error) {
	feed := make(chan *HTMLPage, numFetcher)
	ret, retErr := spawnFetcher(done, feed)

	pc := &PageChannel{send: feed, rec: ret, mutex: &sync.Mutex{}}

	errc := make(chan error)
	go func() {
		defer close(errc)
		in, err := os.OpenFile(filename, os.O_RDONLY, 0644)
		if err != nil {
			errc <- fmt.Errorf("Error reading url list: %v", err)
			return
		}
		defer in.Close()
		reader := bufio.NewReader(in)

		validURL := regexp.MustCompile(`^/p/([0-9]+)$`)
		var wg sync.WaitGroup

		var line string
		for isEOF := false; !isEOF; {
			line, err = reader.ReadString('\n')
			if err != nil {
				isEOF = true
			}
			u, err := url.Parse(strings.TrimSpace(line))
			if err != nil {
				log.Printf("[Fetch] Error parsing %s, skipping\n", line)
				continue
			}

			if u.Host != "tieba.baidu.com" {
				log.Printf("[Fetch] %s is not from Tieba, skipping\n", u)
				continue
			}

			if match := validURL.Match([]byte(u.Path)); !match {
				log.Printf("[Fetch] %s is not a valid Tieba post URL, skipping\n", u)
				continue
			}

			u.RawQuery = ""

			log.Printf("[Fetch] Got new url from list: %v\n", u)

			wg.Add(1)
			go func(u *url.URL) {
				defer wg.Done()
				pc.Add(1)
				select {
				case pc.send <- &HTMLPage{URL: u, Type: HTMLWebHomepage}:
					return
				case <-done:
					return
				}
			}(u)
		}
		wg.Wait()
		pc.Inited()
	}()

	errChan := make(chan error)
	go func() {
		defer close(errChan)
		for {
			if errc == nil && retErr == nil {
				return
			}
			select {
			case err, ok := <-errc:
				if !ok {
					errc = nil
					continue
				}
				errChan <- err
				return

			case err, ok := <-retErr:
				if !ok {
					retErr = nil
					continue
				}
				errChan <- err
				return
			}
		}
	}()

	return pc, errChan
}

func fetcher(done <-chan struct{}, wg *sync.WaitGroup, mutex *sync.Mutex, jobsLeft *int, ret chan<- *HTMLPage, jobs chan *HTMLPage) {
	defer wg.Done()
	for {
		select {
		case <-done:
			return
		case page, ok := <-jobs:
			if !ok {
				return
			}
			// err := fetchHTMLFromURL(page)
			var err error
			if err != nil {
				go func(page *HTMLPage) {
					select {
					case <-done:
						return
					case <-time.After(10 * time.Second):
						jobs <- page
					}
				}(page)
				log.Printf("[Fetch] error fetching %s, pause for 10s: %s\n", page.URL, err)
			} else {
				select {
				case ret <- page:
					mutex.Lock()
					*jobsLeft--
					mutex.Unlock()
				case <-done:
					return
				}
			}
		}
	}
}

func spawnFetcher(done <-chan struct{}, jobs <-chan *HTMLPage) (<-chan *HTMLPage, <-chan error) {
	in := make(chan *HTMLPage, numFetcher)
	ret := make(chan *HTMLPage, numParser)
	errc := make(chan error)

	jobsLeft := 0
	chClosed := false
	var mutex = &sync.Mutex{}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(in)
		defer close(ret)
		for {
			if chClosed {
				mutex.Lock()
				l := jobsLeft
				mutex.Unlock()
				if l <= 0 {
					return
				}
				time.Sleep(time.Second)
				continue
			}
			select {
			case <-done:
				return
			case p, ok := <-jobs:
				if !ok {
					chClosed = true
					continue
				}
				mutex.Lock()
				jobsLeft++
				mutex.Unlock()
				in <- p
			}
		}
	}()
	for i := 0; i < numFetcher; i++ {
		wg.Add(1)
		go fetcher(done, &wg, mutex, &jobsLeft, ret, in)
	}
	go func() {
		wg.Wait()
		close(errc)
	}()
	return ret, errc
}

func fetchHTMLFromURL(page *HTMLPage) error {
	resp, err := http.Get(page.URL.String())
	if err != nil {
		return err
	}
	bytes, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	page.Content = bytes
	return nil
}
