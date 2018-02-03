package main

import (
	"encoding/json"
	"html/template"
	"net/url"
	"sync"
	"sync/atomic"
)

// PageChannel share HTML task between fetcher and parser
type PageChannel struct {
	// parser get HTML pages from rec
	rec <-chan *HTMLPage

	// fetcher get URL from send
	send chan<- *HTMLPage

	// number of URL to be fetched and parsed
	ref int64

	// flag, whether all URLs from list are added to fetcher
	init int64
}

// Add task number
func (p *PageChannel) Add(n int64) {
	if n <= 0 {
		return
	}
	atomic.AddInt64(&p.ref, n)
}

// Del task number
func (p *PageChannel) Del(n int64) {
	if n <= 0 {
		return
	}
	atomic.AddInt64(&p.ref, -n)
}

// Ref returns task number
func (p *PageChannel) Ref() int64 {
	return atomic.LoadInt64(&p.ref)
}

// Inited returns whether all URLs are read from url.txt
func (p *PageChannel) Inited() {
	atomic.StoreInt64(&p.init, 1)
}

// IsDone returns whether all HTML page are fetched
func (p *PageChannel) IsDone() bool {
	return atomic.LoadInt64(&p.ref) <= 0 && atomic.LoadInt64(&p.init) != 0
}

// HTMLType tells parser how to parse the HTMLPage
type HTMLType int

const (
	// HTMLWebHomepage is the first page of a Tieba post
	HTMLWebHomepage HTMLType = iota

	// HTMLWebPage is a page of a Tieba post
	HTMLWebPage

	// HTMLJSON is the Lzl Comment in JSON format
	HTMLJSON

	// HTMLLocal is a local HTML or JSON file
	HTMLLocal
)

// HTMLPage is a job for fetcher and parser
type HTMLPage struct {
	// URL of the Page
	URL *url.URL

	// Content is the HTML code of the Page
	Content []byte

	// Type indicates different types of Tieba data
	Type HTMLType

	// Close http.Response when finished parsing
	// Response *http.Response
}

// TiebaField parse "data-field" of each thread
type TiebaField struct {
	Author struct {
		UserID   uint64 `json:"user_id"`
		UserName string `json:"user_name"` // 用户名
		// Props string `json:"props"`
	} `json:"author"`
	Content struct {
		PostID uint64 `json:"post_id"`
		// IsAnonym bool `json:"is_anonym"`
		ForumID  uint64 `json:"forum_id"`
		ThreadID uint64 `json:"thread_id"`
		Content  string `json:"content"` // 正文内容
		PostNO   uint64 `json:"post_no"` // 楼数
		// Type string `json:"type"`
		// CommentNum uint16 `json:"comment_num"`
		// Props string `json:"props"`
		// PostIndex uint64 `json:"post_index"`
		// PbTpoint *uint64 `json:"pb_tpoint"`
	} `json:"content"`
}

// LzlField parse Lzl JSON data
type LzlField struct {
	ErrNO  int64                      `json:"errno"`
	ErrMsg string                     `json:"errmsg"`
	Data   map[string]json.RawMessage `json:"data"`
}

// LzlContent is a thread of Lzl
type LzlContent struct {
	ThreadID  uint64        `json:"thread_id,string"`
	PostID    uint64        `json:"post_id,string"`
	CommentID uint64        `json:"comment_id,string"`
	UserName  string        `json:"username"`
	Content   template.HTML `json:"content"`
	Timestamp int64         `json:"now_time"`
}

// LzlComment indicates the relationship between a Tieba posts and the attached Lzl comment
type LzlComment struct {
	Num     uint64       `json:"comment_num"`
	ListNum uint64       `json:"comment_list_num"`
	Info    []LzlContent `json:"comment_info"`
	// Info []json.RawMessage `json:"comment_info"`
}

// OutputField render Tieba post in template
type OutputField struct {
	UserName string
	Content  template.HTML
	PostNO   uint64
	PostID   uint64
	Time     string
}

// LzlMap provides a thread safe map insert method
type LzlMap struct {
	Map  map[uint64]*LzlComment
	lock *sync.Mutex
}

// Insert LzlComment to Map with synchronization
func (lzl *LzlMap) Insert(k uint64, v *LzlComment) {
	lzl.lock.Lock()
	lzl.Map[k] = v
	lzl.lock.Unlock()
}

// TemplateField stores all necessary information to render a HTML page
type TemplateField struct {
	Title     string
	ThreadID  uint64
	Posts     []*OutputField
	postsLeft int64
	Lzls      *LzlMap // Key is PostID
	lzlsLeft  int64
	mutex     *sync.RWMutex
	send      bool
}

// Send parsed Tieba posts to render
// https://misfra.me/optimizing-concurrent-map-access-in-go/
func (t *TemplateField) Send(c chan *TemplateField) {
	t.mutex.RLock()
	if !t.send {
		t.mutex.RUnlock()
		t.mutex.Lock()
		if !t.send {
			c <- t
			t.send = true
		}
		t.mutex.Unlock()
	} else {
		t.mutex.RUnlock()
	}
}

// AddPosts adds the number of Posts to be parsed
func (t *TemplateField) AddPosts(n int64) {
	atomic.AddInt64(&t.postsLeft, n)
}

// AddLzls adds the number of Lzls to be parsed
func (t *TemplateField) AddLzls(n int64) {
	atomic.AddInt64(&t.lzlsLeft, n)
}

// Append a new post to TemplateField
func (t *TemplateField) Append(post *OutputField) {
	t.mutex.Lock()
	// l := len(t.Posts)
	// n := l + 1
	// if n > cap(t.Posts) {
	// 	newSlice := make([]*OutputField, 30*10+n+1)
	// 	copy(newSlice, t.Posts)
	// 	t.Posts = newSlice
	// }
	// t.Posts = t.Posts[0:n]
	// copy(t.Posts[n:n+1], post)
	t.Posts = append(t.Posts, post)
	t.mutex.Unlock()
}

// IsDone returns whether TemplateField is ready to be rendered
func (t *TemplateField) IsDone() bool {
	return atomic.LoadInt64(&t.postsLeft) <= 0 && atomic.LoadInt64(&t.lzlsLeft) <= 0
}

// Merge consecutive posts whose Useaname is the same
func (t *TemplateField) Merge() {
	l := len(t.Posts)
	for i := 0; i+1 < l; i++ {
		if t.Posts[i+1].UserName != t.Posts[i].UserName {
			continue
		}
		v, ok := t.Lzls.Map[t.Posts[i+1].PostID]
		if ok && v.ListNum != 0 && v.Num != 0 {
			continue
		}
		v, ok = t.Lzls.Map[t.Posts[i].PostID]
		if ok && v.ListNum != 0 && v.Num != 0 {
			continue
		}
		// How to efficiently concatenate strings in Go?
		// https://stackoverflow.com/a/43675122/6091246
		bs := make([]byte, len(t.Posts[i].Content)+len(t.Posts[i+1].Content)+1)
		bl := 0
		bl += copy(bs[bl:], t.Posts[i].Content)
		bs[bl] = '\n'
		bl++
		bl += copy(bs[bl:], t.Posts[i+1].Content)
		t.Posts[i].Content = template.HTML(bs)
		// t.Posts[i].Content = t.Posts[i].Content + "\n" + t.Posts[i+1].Content
		// removes duplicate values in given slice
		// https://gist.github.com/alioygur/16c66b4249cb42715091fe010eec7e33#file-unique_slice-go-L13
		t.Posts = append(t.Posts[:i+1], t.Posts[i+2:]...)
		i--
		l--
	}
}

// Unique removes any duplicate posts
// too naive
// TODO: improve result with NLP technique
func (t *TemplateField) Unique() {
	// Idiomatic way to remove duplicates in a slice
	// https://www.reddit.com/r/golang/comments/5ia523/idiomatic_way_to_remove_duplicates_in_a_slice/db6qa2e/
	seen := make(map[template.HTML]struct{}, len(t.Posts))
	j := 0
	for _, v := range t.Posts {
		if _, ok := seen[v.Content]; ok {
			continue
		}
		seen[v.Content] = struct{}{}
		t.Posts[j] = v
		j++
	}
	t.Posts = t.Posts[:j]
}

// TemplateMap manipulate a Tieba thread in parser
type TemplateMap struct {
	Map     map[uint64]*TemplateField // Key is ThreadID
	lock    *sync.RWMutex
	Channel chan *TemplateField
}

// Get returns a value from Map with synchronization
// see: https://misfra.me/optimizing-concurrent-map-access-in-go/ for more detail
func (tm *TemplateMap) Get(k uint64) *TemplateField {
	var val *TemplateField
	var ok bool
	tm.lock.RLock()
	if val, ok = tm.Map[k]; !ok {
		tm.lock.RUnlock()
		tm.lock.Lock()
		if val, ok = tm.Map[k]; !ok {
			val = &TemplateField{
				ThreadID: k,
				Posts:    make([]*OutputField, 0, 30),
				Lzls: &LzlMap{
					Map:  make(map[uint64]*LzlComment),
					lock: &sync.Mutex{},
				},
				mutex: &sync.RWMutex{},
			}
			tm.Map[k] = val
		}
		tm.lock.Unlock()
	} else {
		tm.lock.RUnlock()
	}
	return val
}
