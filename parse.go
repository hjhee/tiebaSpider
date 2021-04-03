package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"math/rand"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randStringRunes(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func htmlParseWrapperFcn(done <-chan struct{}, pc *PageChannel, page *HTMLPage, tmMap *TemplateMap, querySelector, threadSelector string, callback func(tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error) error {
	defer pc.Del(1)
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page.Content))
	if err != nil {
		// network error, retry request
		pc.Add(1)
		go addPageToFetchQueue(done, pc, time.Duration(retryPeriod)*time.Second, page.URL, page.Type)
		return fmt.Errorf("error parsing %s(title: %s): %v", page.URL, findTitle(doc), err)
	}

	posts := doc.Find(querySelector)
	threadRegex := regexp.MustCompile(threadSelector)
	match := threadRegex.FindStringSubmatch(string(page.Content))
	if len(match) < 1 {
		// network error, retry request
		pc.Add(1)
		go addPageToFetchQueue(done, pc, time.Duration(retryPeriod)*time.Second, page.URL, page.Type)
		return fmt.Errorf("unable to parse page(title: %s), possibly a network error, readding url to queue %s", findTitle(doc), page.URL)
	}
	strInt, _ := strconv.ParseInt(match[1], 10, 64)
	threadID := uint64(strInt)
	tf := tmMap.Get(threadID)
	err = callback(tf, doc, posts)
	// page.Response.Body.Close()
	if err != nil {
		pc.Add(1)
		go addPageToFetchQueue(done, pc, time.Duration(retryPeriod)*time.Second, page.URL, page.Type)
	} else {
		tf.AddPage(-1)
		if tf.IsDone() {
			// fmt.Fprintf(os.Stderr, "IsDone() in htmlParse: %d, %d\n", tf.lzlsLeft, tf.pagesLeft)
			tf.Send(tmMap.Channel) // avoid duplicate task
			// tmMap.Channel <- tf
		}
	}

	return err
}

func htmlParse(done <-chan struct{}, pc *PageChannel, page *HTMLPage, tmMap *TemplateMap, callback func(tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error) error {
	// posts := doc.Find("div.l_post.j_l_post.l_post_bright")
	// threadRegex := regexp.MustCompile(`\b"?thread_id"?:"?(\d+)"?\b`)
	return htmlParseWrapperFcn(done, pc, page, tmMap, "div.l_post.j_l_post.l_post_bright", `\b"?thread_id"?:"?(\d+)"?\b`, callback)
}

func homePageParserFcn(done <-chan struct{}, pc *PageChannel, tf *TemplateField, doc *goquery.Document, posts *goquery.Selection, page *HTMLPage, pageTitleFinder func(doc *goquery.Document) string, pageNumFinder func(doc *goquery.Document) (int64, error), pageType HTMLType, parserFcn func(page *HTMLPage, tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error) error {
	tf.Title = pageTitleFinder(doc)
	log.Printf("[homepage] Title: %s", tf.Title)
	// issue #11 add url to page content
	tf.Url = page.URL.String()

	pageNum, err := pageNumFinder(doc)
	if err != nil {
		return fmt.Errorf("error parsing total number of pages: %v", err)
	}

	tf.pagesLeft = pageNum
	tf.lzlsLeft = pageNum
	// fetch all comments and lzls, excluding comments in the first page
	// pageNum - 1: html page 2~pageNum
	// pageNum + 1: lzl page 0~pageNum
	pc.Add(pageNum - 1 + pageNum + 1)
	go addPageToFetchQueueFromHomePage(done, pc, page.URL, tf.ThreadID, pageNum, pageType)

	return parserFcn(page, tf, doc, posts)
}

func homepageParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	return htmlParse(done, pc, page, tmMap, func(tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error {
		return homePageParserFcn(done, pc, tf, doc, posts, page, findTitle, func(doc *goquery.Document) (int64, error) {
			var pageNum int64
			if s := doc.Find("span.red").Eq(1); s.Text() == "" {
				pageNum = 1 // Could not find total number of pages, default to 1
			} else {
				n, err := strconv.Atoi(s.Text())
				if err != nil {
					return 0, fmt.Errorf("error parsing total number of pages: %v", err)
				}
				pageNum = int64(n)
			}
			return pageNum, nil
		}, HTMLWebPage, pageParserFcn)
	})
}

func pageParserFcn(page *HTMLPage, tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error {
	posts.Each(func(i int, s *goquery.Selection) {
		// filter elements that has more than 4 class (maybe an advertisement, commit 9c82d4e381d1bcd3f801bf5f6c07960fb7d829be)
		classStr, _ := s.Attr("class") // get class string
		if len(strings.Fields(classStr)) > 4 {
			return
		}

		dataField, ok := s.Attr("data-field")
		if !ok {
			// maybe not an error, but an older version of data-field
			fmt.Fprintf(os.Stderr, "#%d data-field not found: %s\n", i, page.URL) // there's a error on the page, maybe Tieba updated the syntax
			return
		}

		var tiebaPost TiebaField
		var res OutputField
		err := json.Unmarshal([]byte(dataField), &tiebaPost)
		if err != nil {
			fmt.Fprintf(os.Stderr, "#%d data-field unmarshal failed: %v, url: %s\n", i, err, page.URL) // there's a error on the page, maybe Tieba updated the syntax
			return
		}
		if content, err := s.Find("div.d_author ul.p_author li.d_name a.p_author_name.j_user_card").Html(); err != nil {
			fmt.Fprintf(os.Stderr, "#%d Error parsing username from %s\n", i, page.URL)
			return
		} else {
			res.UserName = template.HTML(handleUserNameEmojiURL(content))
		}

		res.Content = template.HTML(tiebaPost.Content.Content)
		res.PostNO = tiebaPost.Content.PostNO
		res.PostID = tiebaPost.Content.PostID

		if res.Content == "" {
			// data-field does not contain content
			// infer an old version of posts
			postID := fmt.Sprintf("#post_content_%d", res.PostID)
			content, err := posts.Find(postID).Html()
			if err != nil {
				log.Printf("#%d: post_content_%d parse failed, %s", i, res.PostID, err)
			} else {
				res.Content = template.HTML(content)
			}
		}

		// get post time
		// Jquery过滤选择器，选择前几个元素，后几个元素，内容过滤选择器等
		// http://www.cnblogs.com/alone2015/p/4962687.html
		res.Time = s.Find("span.tail-info:nth-child(4)").Text() // posted from device other than PC
		if res.Time == "" {
			res.Time = s.Find("span.tail-info:nth-child(3)").Text() // posted from PC
		}

		tf.Append(&res)
		// log.Printf("#%d data-field found: %v\n", i, tiebaPost)
		// log.Printf("#%d data-field found:\nauthor: %s\ncontent: %s\n",
		// 	tiebaPost.Content.PostNo,
		// 	tiebaPost.Author.UserName,
		// 	tiebaPost.Content.Content)

		// result.Posts <- &res
	})
	return nil
}

func pageParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	// log.Printf("[Parse] parsing %s", page.URL.String())
	return htmlParse(done, pc, page, tmMap, func(tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) (err error) {
		return pageParserFcn(page, tf, doc, posts)
	})
}

func wapParse(done <-chan struct{}, pc *PageChannel, page *HTMLPage, tmMap *TemplateMap, callback func(tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error) error {
	// posts := doc.Find("div.i")
	// threadRegex := regexp.MustCompile(`kz=(\d+)`)
	return htmlParseWrapperFcn(done, pc, page, tmMap, "div.i", `kz=(\d+)`, callback)
}

func wapHomePageParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	return wapParse(done, pc, page, tmMap, func(tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error {
		return homePageParserFcn(done, pc, tf, doc, posts, page, findWapTitle, func(doc *goquery.Document) (int64, error) {
			var pageNum int64
			pageNumMatcher := regexp.MustCompile(`第\d+/(\d+)页`)
			matches := pageNumMatcher.FindStringSubmatch(doc.Find("div.h").Text())
			if len(matches) > 1 {
				n, err := strconv.Atoi(matches[1])
				if err != nil {
					return 0, fmt.Errorf("error parsing total number of pages: %v", err)
				}
				pageNum = int64(n)
			} else {
				pageNum = 1 // Could not find total number of pages, default to 1
			}
			return pageNum, nil
		}, HTMLWebWAPPage, wapParserFcn)
	})
}

func wapParserFcn(page *HTMLPage, tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) error {
	postMatcher := regexp.MustCompile(`^(\d+)楼. (.*)<br/>`)
	posts.Each(func(i int, s *goquery.Selection) {
		var res OutputField
		if content, err := s.Find(".g > a").Html(); err != nil {
			fmt.Fprintf(os.Stderr, "#%d Error parsing username from %s\n", i, page.URL)
			return
		} else {
			res.UserName = template.HTML(handleUserNameEmojiURL(content))
		}

		sBody, _ := s.Html()
		sTable, _ := s.Find("table").Html()
		sContent := strings.ReplaceAll(sBody, fmt.Sprintf("<table>%s</table>", sTable), "")
		if matches := postMatcher.FindStringSubmatch(string(sContent)); len(matches) < 3 {
			fmt.Fprintf(os.Stderr, "#%d Error parsing post content from %s\n", i, page.URL)
			return
		} else {
			n, err := strconv.Atoi(matches[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "error parsing post number: %v", err)
				return
			}
			res.PostNO = uint64(n)
			res.Content = template.HTML(matches[2])
		}
		// res.PostID = tiebaPost.Content.PostID
		if sReply, ok := s.Find(".r>a").Attr("href"); ok {
			if replyUrl, err := url.Parse(sReply); err == nil {
				pid := replyUrl.Query().Get("pid")
				if n, err := strconv.Atoi(pid); err == nil {
					res.PostID = uint64(n)
				}
			}
		}
		res.Time = s.Find(".b").Text()

		tf.Append(&res)
	})
	return nil
}

func wapPageParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	// log.Printf("[Parse] parsing %s", page.URL.String())
	return wapParse(done, pc, page, tmMap, func(tf *TemplateField, doc *goquery.Document, posts *goquery.Selection) (err error) {
		return wapParserFcn(page, tf, doc, posts)
	})
}

// parse lzl comment, JSON formatted
func jsonParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap, callback func(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap, tf *TemplateField) error) error {
	defer pc.Del(1)
	u := page.URL
	q := u.Query()
	tid := q.Get("tid")
	if tid == "" {
		return fmt.Errorf("error parsing getting tid from %s", page.URL) // skip illegal URL
	}
	ret, _ := strconv.Atoi(tid)
	threadID := uint64(ret)

	tf := tmMap.Get(threadID)
	defer tf.AddLzl(-1)
	err := callback(done, page, pc, tmMap, tf)
	if err != nil {
		pc.Add(1)
		tf.AddLzl(1)
		go addPageToFetchQueue(done, pc, time.Duration(retryPeriod)*time.Second, page.URL, page.Type)
	} else {
		if tf.IsDone() {
			// fmt.Fprintf(os.Stderr, "IsDone() in jsonParser: %d, %d\n", tf.lzlsLeft, tf.pagesLeft)
			tf.Send(tmMap.Channel) // avoid duplicate task
			// tmMap.Channel <- tf
		}
	}
	return err
}

func requestLzlComment(tid string, pid string, pn string, tp HTMLType, pc *PageChannel) {
	// there are more lzls to fetch
	// url syntax:
	// url example: https://tieba.baidu.com/p/comment?tid=7201761174&pn=4
	u := &url.URL{
		Scheme: "http",
		Host:   "tieba.baidu.com",
		Path:   "/p/comment",
	}
	q := u.Query()
	// q.Set("t", strconv.Itoa(int(time.Now().UnixNano()/1000000)))
	q.Set("tid", tid)
	// q.Set("pid", pid)
	q.Set("pn", pn) // start fetching additional comment from page 2
	u.RawQuery = q.Encode()

	// log.Printf("requesting %s", u)

	pc.send <- &HTMLPage{
		URL:  u,
		Type: tp,
	}
}

func totalCommentParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	return jsonParser(done, page, pc, tmMap, func(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap, tf *TemplateField) error {
		url := page.URL.String()
		body := string(page.Content)
		var lzl LzlField
		var err error
		contentCandidates := make(chan string, 10)
		contentCandidates <- body
		for len(contentCandidates) > 0 {
			contentBuffer := <-contentCandidates
			err := json.Unmarshal([]byte(contentBuffer), &lzl)
			if err != nil {
				switch err := err.(type) {
				default:
					if len(contentCandidates) == 0 {
						return fmt.Errorf("error parsing content file %s: %v", url, err)
					}
				case *json.SyntaxError:
					// handle corrupted json data, as in #12
					// example: https://tieba.baidu.com/p/totalComment?fid=572638&pn=0&t=1617364074015&tid=6212415344&red_tag=3017655123
					if contentBuffer[:err.Offset-1] != `{"errno":null,"errmsg":null}` {
						fmt.Fprintf(os.Stderr, "[Parser] warning: lzl data corrupted: %s: %s, trying to reparse strings between offset %d", url, contentBuffer, err.Offset)
						contentCandidates <- contentBuffer[:err.Offset-1]
					}
					contentCandidates <- contentBuffer[err.Offset-1:]
				}
			}
			if lzl.ErrMsg == "success" {
				break
			}
		}
		if lzl.ErrMsg != "success" {
			return fmt.Errorf("unable to find json lzl with ErrMsg(\"success\"), last message was: %s", body)
		}
		if lzl.ErrNO != 0 {
			return fmt.Errorf("error getting data: %s, %s", url, lzl.ErrMsg)
		}
		commentList, ok := lzl.Data["comment_list"]
		if !ok {
			return fmt.Errorf("error getting comment_list: %s", url)
		}
		if string(commentList) == "" || string(commentList) == "[]" {
			return nil // comment list empty, stop
		}
		comments := make(map[uint64]*LzlComment)
		err = json.Unmarshal([]byte(string(commentList)), &comments)
		if err != nil {
			return fmt.Errorf("error parsing comment_list from %s: %v\ncomment_list:\n%s", url, err, commentList)
		}

		if len(comments) == 0 {
			return nil // does not have any comments, stop
		}

		for pid, v := range comments {
			if tf.Lzls.IsExist(pid) {
				// totalComment contains lzls in different pages, which are duplicate
				continue
			}
			// normalize
			for i, comment := range v.Info {
				comment.Index = int64(i)
				comment.Time = time.Unix(comment.Timestamp, 0).In(time.Local).Format("2006-01-02 15:04")
				comment.UserName = handleUserNameEmojiURL(comment.UserName)
				comment.Content = template.HTML(reformatLzlUsername(string(comment.Content)))
			}
			// merge maps
			// Getting the union of two maps in go
			// https://stackoverflow.com/a/22621838/6091246
			numLeft := int64(v.Num) - int64(v.ListNum)
			if numLeft > 0 {
				// extend Lzl slice if needed
				if n := len(v.Info); uint64(n) < v.Num {
					// extend slice
					newSlice := make([]*LzlContent, n, v.Num+1)
					copy(newSlice, v.Info)
					v.Info = newSlice
				}
				pc.Add(1)
				tf.AddLzl(1)
				go requestLzlComment(strconv.Itoa(int(tf.ThreadID)), strconv.Itoa(int(pid)), "2", HTMLLzlHome, pc)
			}
			tf.Lzls.Insert(pid, v) // merge maps
		}
		return nil
	})
}

func commentParserFcn(url *url.URL, body string, pageType HTMLType, appendLzl func(key uint64, value *LzlContent), requestLzlCommentFcn func(tid, pid, pageNum string)) error {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("error parsing %s: %v", url.String(), err)
	}
	q := url.Query()
	tid := q.Get("tid")
	pid := q.Get("pid")
	pn := q.Get("pn")
	if pageType == HTMLLzlHome {
		s := doc.Find("li.lzl_li_pager_s")
		dataField, ok := s.Attr("data-field")
		if !ok {
			return fmt.Errorf("error parsing %s: total number of pages is not determinable", url)
		}
		var lzlPage LzlPageComment
		err := json.Unmarshal([]byte(dataField), &lzlPage)
		if err != nil {
			return fmt.Errorf("LzlPageComment data-field unmarshal failed: %v, url: %s", err, url)
		}
		// tf.AddLzl(int64(lzlPage.TotalPage - 2))
		for i := uint64(3); i <= lzlPage.TotalPage; i++ {
			requestLzlCommentFcn(tid, pid, strconv.Itoa(int(i)))
			// requestLzlComment(tid, pid, strconv.Itoa(int(i)), HTMLLzl, pc)
		}
	}
	exLzls := doc.Find(".lzl_single_post.j_lzl_s_p")
	exLzls.Each(func(i int, s *goquery.Selection) {
		pageNum, _ := strconv.Atoi(pn)
		key, _ := strconv.Atoi(pid)
		content, err := s.Find(".lzl_content_main").Html()
		if err != nil {
			return
		}
		user := s.Find("div.lzl_cnt a.at.j_user_card")
		userName := user.Text()
		// userName, ok := user.Attr("username")
		// if !ok {
		// 	// userName not found
		// 	log.Printf("ExLzl: cannot find username for pid=%s, index=%d", pid, i+pageNum*10)
		// 	return
		// } else
		if userName == "" {
			// user name is empty, try another method
			log.Printf("ExLzl: please check url: %s", url)
			return
		}
		c := &LzlContent{
			Index:    int64(i + pageNum*10),
			UserName: handleUserNameEmojiURL(userName),
			Content:  template.HTML(reformatLzlUsername(content)),
			Time:     s.Find(".lzl_time").Text(),
		}
		appendLzl(uint64(key), c)
		// tf.Lzls.Append(uint64(key), c)
	})
	return nil
}

func commentParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	return jsonParser(done, page, pc, tmMap, func(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap, tf *TemplateField) error {
		return commentParserFcn(page.URL, string(page.Content), page.Type, func(key uint64, value *LzlContent) {
			tf.Lzls.Append(uint64(key), value)
		}, func(tid, pid, pageNum string) {
			pc.Add(1)
			tf.AddLzl(1)
			go requestLzlComment(tid, pid, pageNum, HTMLLzl, pc)
		})
	})
}

// parse templateField from local file, JSON formatted
func templateParser(done <-chan struct{}, page *HTMLPage, pc *PageChannel, tmMap *TemplateMap) error {
	defer pc.Del(1)
	var threadID uint64

	u := page.URL
	q := u.Query()
	tid := q.Get("tid")
	if tid == "" {
		return fmt.Errorf("error parsing getting tid from %s", page.URL.String()) // skip illegal URL
	}
	ret, _ := strconv.Atoi(tid)
	threadID = uint64(ret)

	var tf = tmMap.Get(threadID)

	tf.mutex.Lock()
	err := json.Unmarshal([]byte(string(page.Content)), tf)
	tf.mutex.Unlock()
	if err != nil {
		return fmt.Errorf("error parsing template file %s: %v", page.URL.String(), err)
	}
	tf.Send(tmMap.Channel)

	return nil
}

func parser(done <-chan struct{}, errc chan<- error, wg *sync.WaitGroup, pc *PageChannel, tmMap *TemplateMap) {
	defer wg.Done()
	var err error
	for {
		select {
		case <-done:
			return
		case p, ok := <-pc.rec:
			if !ok {
				return // quit when pc.rec is closed
			}
			switch p.Type {
			case HTMLWebHomepage:
				err = homepageParser(done, p, pc, tmMap)
				if err != nil {
					errc <- err
				}
			case HTMLWebPage:
				err = pageParser(done, p, pc, tmMap)
				if err != nil {
					errc <- err
				}
			case HTMLJSON:
				err = totalCommentParser(done, p, pc, tmMap)
				if err != nil {
					errc <- err
				}
			case HTMLLzlHome, HTMLLzl:
				err = commentParser(done, p, pc, tmMap)
				if err != nil {
					errc <- err
				}
			case HTMLLocal:
				err = templateParser(done, p, pc, tmMap)
				if err != nil {
					errc <- err
				}
			case HTMLWebWAPHomepage:
				err = wapHomePageParser(done, p, pc, tmMap)
				if err != nil {
					errc <- err
				}
			case HTMLWebWAPPage:
				err = wapPageParser(done, p, pc, tmMap)
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
	tmMap := &TemplateMap{
		Map:     make(map[uint64]*TemplateField),
		lock:    &sync.RWMutex{},
		Channel: make(chan *TemplateField, numRenderer),
	}
	errc := make(chan error)

	var wg sync.WaitGroup
	wg.Add(numParser)
	for i := 0; i < numParser; i++ {
		go parser(done, errc, &wg, pc, tmMap)
	}
	go func() {
		for {
			log.Printf("[pc] jobs: %d", pc.Ref()) // status report
			if pc.IsDone() {
				close(pc.send) // no more task, tell fetcher to exit
				break
			}
			time.Sleep(time.Second) // check task number every second
		}
		wg.Wait() // wait parser finish all remaining tasks
		close(errc)
		close(tmMap.Channel) // all page parsed, tell renderer to exit
	}()
	return tmMap.Channel, errc
}

func findTitle(doc *goquery.Document) string {
	var title string
	if s := doc.Find("title"); s.Text() == "" {
		title = randStringRunes(15) // Could not find title, default to random
	} else {
		title = s.Text()
	}
	return title
}

func findWapTitle(doc *goquery.Document) string {
	var title string
	if s := doc.Find(".bc > strong:nth-child(1)"); s.Text() == "" {
		title = randStringRunes(15) // Could not find title, default to random
	} else {
		title = s.Text()
	}
	if s := doc.Find("div.d.h ~ a").First(); s.Text() != "" {
		title = fmt.Sprintf("%s_%s_wap", title, s.Text())
	}
	return title
}

func addPageToFetchQueue(done <-chan struct{}, pc *PageChannel, delay time.Duration, url *url.URL, pageType HTMLType) {
	if delay > 0 {
		select {
		case <-done:
			return
		case <-time.After(delay):
		}
	}
	// add failed task back to jobs
	select {
	case <-done:
		return
	case pc.send <- &HTMLPage{
		URL:  url,
		Type: pageType,
	}:
	}
}

func reformatLzlUsername(content string) string {
	// special rule: remove username ahref in ": 回复 ", as requested in #4
	content = strings.Trim(content, " ")
	// fmt.Fprintf(os.Stderr, "before: %s\n", content)
	if strings.HasPrefix(content, "回复") {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
		if err == nil {
			bodyDOM := doc.Find("body")
			s := doc.Find("a.at").First()
			userNameHtml, _ := s.Html()
			// t.Errorf(userNameHtml)
			s.ReplaceWithHtml(userNameHtml)
			content, _ = bodyDOM.Html()
			// t.Errorf("failed to parse comment data: %v, reason: %s", content, err)
			// fmt.Fprintf(os.Stderr, "after: %s\n", content)
		}
	}
	return content
}

func handleUserNameEmojiURL(userName string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(userName))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[handleUserNameEmojiURL] error handling user: %s", userName)
		return userName
	}
	doc.Find("img.nicknameEmoji").Each(func(i int, s *goquery.Selection) {
		if url, ex := s.Attr("src"); ex {
			if strings.HasPrefix(url, "//") {
				// the url needs to add the protocol type
				s.SetAttr("src", "https:"+url)
			}
		}
	})
	if content, err := doc.Find("body").Html(); err == nil {
		return content
	}
	content, _ := doc.Html()
	return content
}

func addPageToFetchQueueFromHomePage(done <-chan struct{}, pc *PageChannel, urlRef *url.URL, tid uint64, pageNum int64, pageType HTMLType) {
	for i := int64(2); i <= pageNum; i++ {
		u := &url.URL{}
		*u = *urlRef
		q := u.Query()
		switch pageType {
		case HTMLWebPage:
			q.Set("pn", strconv.Itoa(int(i)))
		case HTMLWebWAPPage:
			q.Set("pnum", strconv.Itoa(int(i)))
		}
		u.RawQuery = q.Encode()
		newPage := &HTMLPage{
			URL:  u, // example: http://tieba.baidu.com/mo/m?kz=7201761174&pnum=2
			Type: pageType,
		}
		select {
		case <-done:
			return
		case pc.send <- newPage: // add all other pages to fetcher
		}
	}

	// forumRegex := regexp.MustCompile(`\b"?forum_id"?:"?(\d+)"?\b`)
	// match := forumRegex.FindStringSubmatch(string(page.Content))
	// strInt, _ := strconv.ParseInt(match[1], 10, 64)
	// forumID := uint64(strInt)
	// fetch lzl comments
	// syntax:
	// http://tieba.baidu.com/p/totalComment?t=15769421323&tid=7201761174&fid=572638&pn=2&see_lz=0
	// python爬取贴吧楼中楼
	// https://mrxin.github.io/2015/09/19/tieba-louzhonglou/
	for i := int64(0); i <= pageNum; i++ {
		u := &url.URL{
			Scheme: "http",
			Host:   "tieba.baidu.com",
			Path:   "/p/totalComment",
		}
		q := u.Query()
		// Go by Example: Epoch
		// https://gobyexample.com/epoch
		// q.Set("t", strconv.Itoa(int(time.Now().UnixNano()/1000000)))
		q.Set("tid", strconv.Itoa(int(tid)))
		// q.Set("fid", strconv.Itoa(int(forumID)))
		q.Set("pn", strconv.Itoa(int(i)))
		u.RawQuery = q.Encode()
		// log.Printf("requesting totalComment: %s", u)
		select {
		case <-done:
			return
		case pc.send <- &HTMLPage{
			URL:  u,
			Type: HTMLJSON,
		}:
		}
	}
}
