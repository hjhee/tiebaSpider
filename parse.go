package main

import (
	"time"
	"encoding/json"
	"errors"
	"github.com/PuerkitoBio/goquery"
	"html"
	"html/template"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"strconv"
	"sync"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func homepageParser(pages *HTMLPage, pc *PageChannel, tempc chan<- *TemplateField) error {
	log.Printf("[Parser] Got %s\n", pages.URL)
	pc.Del(1)
	// close(fetcher)
	return nil
}

func pageParser(pages *HTMLPage, pc *PageChannel, tempc chan<- *TemplateField) error {
	return nil
}

func commentParser(pages *HTMLPage, pc *PageChannel, tempc chan<- *TemplateField) error {
	return nil
}

func htmlParser(done <-chan struct{}, errc chan<- error, wg *sync.WaitGroup, pc *PageChannel, tempc chan<- *TemplateField) {
	defer wg.Done()
	var err error
	for {
		select {
		case <-done:
			return
		case p, ok := <-pc.rec:
			if !ok {
				return
			}
			switch p.Type {
			case HTMLWebHomepage:
				err = homepageParser(p, pc, tempc)
				if err != nil {
					errc <- err
				}
			case HTMLWebPage:
				err = pageParser(p, pc, tempc)
				if err != nil {
					errc <- err
				}
			case HTMLJson:
				err = commentParser(p, pc, tempc)
				if err != nil {
					errc <- err
				}
			default:
				errc <- errors.New("unkonwn HTMLPage Type")
			}
		}
	}
}

func parseHTML(done <-chan struct{}, pc *PageChannel) (<-chan *TemplateField, <-chan error) {
	tempc := make(chan *TemplateField, numGenerator)
	errc := make(chan error)

	var wg sync.WaitGroup
	wg.Add(numParser)
	for i := 0; i < numParser; i++ {
		go htmlParser(done, errc, &wg, pc, tempc)
	}
	go func() {
		for {
			if pc.IsDone() {
				close(pc.send)
				break
			}
			time.Sleep(time.Second)
		}
		wg.Wait()
		close(errc)
	}()
	return tempc, errc
}

func parseHTMLFromFile(filename string) (*TemplateField, chan error) {
	result := &TemplateField{
		Posts: make(chan *OutputField),
	}
	errc := make(chan error)

	go func() {
		var wg sync.WaitGroup
		f, err := os.OpenFile(filename, os.O_RDONLY, 0644)
		defer f.Close()
		if err != nil {
			log.Printf("Error opening content file %s: %v", filename, err)
			errc <- err
		}
		doc, err := goquery.NewDocumentFromReader(f)
		if err != nil {
			log.Printf("Error parsing content file %s: %v", filename, err)
			errc <- err
		}

		var title string
		if s := doc.Find("title"); s.Text() == "" {
			title = randStringRunes(15)
			log.Printf("Could not find title, default to %v", title)
		} else {
			title = s.Text()
			log.Printf("title: %s\n", title)
		}
		result.Title = title

		var pageNum uint16
		if s := doc.Find("span.red").Eq(1); s.Text() == "" {
			log.Printf("Could not find total number of pages, default to 1")
			pageNum = 1
		} else {
			n, err := strconv.Atoi(s.Text())
			if err != nil {
				log.Printf("Error parsing total number of pages: %v", err)
				errc <- err
			}
			pageNum = uint16(n)
			log.Printf("total number of pages: %d\n", pageNum)
		}

		posts := doc.Find("div.l_post.l_post_bright.j_l_post.clearfix")
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := posts.First()
			dataField, ok := s.Attr("data-field")
			if !ok {
				log.Printf("first data-field not found!\n")
				return
			}
			var tiebaPost TiebaField
			err := json.Unmarshal([]byte(dataField), &tiebaPost)
			if err != nil {
				log.Printf("first data-field unmarshal failed: %v", err)
				return
			}
			threadID := tiebaPost.Content.ThreadID
			result.Lzls, errc = parseJSONFromFile(threadID, "example/lzl.json")
			go func() {
				done := false
				for !done {
					select {
					case err, ok := <-errc:
						if !ok {
							done = true
						} else {
							log.Printf("error processing lzl: %v", err)
							done = true
						}
					}
				}
			}()
		}()

		posts.Each(func(i int, s *goquery.Selection) {
			dataField, ok := s.Attr("data-field")
			if !ok {
				log.Printf("#%d data-field not found!\n", i)
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				var tiebaPost TiebaField
				var res OutputField
				err := json.Unmarshal([]byte(dataField), &tiebaPost)
				if err != nil {
					log.Printf("#%d data-field unmarshal failed: %v", i, err)
					return
				}

				res.UserName = tiebaPost.Author.UserName
				res.Content = template.HTML(html.UnescapeString(tiebaPost.Content.Content))
				res.PostNO = tiebaPost.Content.PostNO
				res.PostID = tiebaPost.Content.PostID
				// log.Printf("#%d data-field found: %v\n", i, tiebaPost)
				// log.Printf("#%d data-field found:\nauthor: %s\ncontent: %s\n",
				// 	tiebaPost.Content.PostNo,
				// 	tiebaPost.Author.UserName,
				// 	tiebaPost.Content.Content)

				tiebaPost = TiebaField{}
				result.Posts <- &res
			}()
		})

		go func() {
			wg.Wait()
			close(result.Posts)
			log.Printf("channel result closed\n")
		}()
	}()
	return result, errc
}

func parseJSONFromFile(threadID uint64, filename string) (chan map[uint64]*LzlComment, chan error) {
	log.Printf("thread_id: %d", threadID)
	ret := make(chan map[uint64]*LzlComment)
	errc := make(chan error)
	go func() {
		f, err := os.OpenFile(filename, os.O_RDONLY, 0644)
		defer f.Close()
		if err != nil {
			log.Printf("Error opening content file %s: %v", filename, err)
			errc <- err
		}
		b, err := ioutil.ReadAll(f)
		if err != nil {
			log.Printf("Error reading content file %s: %v", filename, err)
			errc <- err
		}
		var lzl LzlField
		err = json.Unmarshal(b, &lzl)
		if err != nil {
			log.Printf("Error parsing content file %s: %v", filename, err)
			errc <- err
		}
		if lzl.ErrNO != 0 {
			log.Printf("Error getting data: %s\n", lzl.ErrMsg)
			errc <- errors.New(lzl.ErrMsg)
		}
		commentList, ok := lzl.Data["comment_list"]
		if !ok {
			log.Printf("Error getting comment_list: %s", filename)
			errc <- errors.New("Error getting comment_list")
		}
		comments := make(map[uint64]*LzlComment)
		// var comments LzlComment
		err = json.Unmarshal(commentList, &comments)
		if err != nil {
			log.Printf("Error parsing comment_list from file %s: %v", filename, err)
			errc <- err
		}
		ret <- comments
		close(ret)
	}()
	// log.Printf("json comment list: %s", comments[72294350192])
	return ret, errc
}
