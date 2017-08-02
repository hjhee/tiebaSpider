package main

import (
	"encoding/json"
	"html/template"
	"net/url"
	"sync"
)

type PageChannel struct {
	rec <-chan *HTMLPage
	send chan<- *HTMLPage
	ref int
	init bool
	mutex *sync.Mutex
}

func (p *PageChannel) Add(n int) {
	p.mutex.Lock()
	p.ref += n
	p.mutex.Unlock()
}

func (p *PageChannel) Del(n int) {
	p.mutex.Lock()
	p.ref -= n
	p.mutex.Unlock()
}

func (p *PageChannel) Ref() int {
	var r int
	p.mutex.Lock()
	r = p.ref
	p.mutex.Unlock()
	return r
}

func (p *PageChannel) Inited() {
	p.mutex.Lock()
	p.init = true
	p.mutex.Unlock()
}

func (p *PageChannel) IsDone() bool {
	var r int
	var d bool
	p.mutex.Lock()
	r = p.ref
	d = p.init
	p.mutex.Unlock()
	return r<=0 && d
}

type HTMLType int

const (
	HTMLWebHomepage HTMLType = iota
	HTMLWebPage
	HTMLJson
)

type HTMLPage struct {
	URL     *url.URL
	Content []byte
	Type    HTMLType
}

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
		PostNO   uint32 `json:"post_no"` // 楼数
		// Type string `json:"type"`
		// CommentNum uint16 `json:"comment_num"`
		// Props string `json:"props"`
		// PostIndex uint64 `json:"post_index"`
		// PbTpoint *uint64 `json:"pb_tpoint"`
	} `json:"content"`
}

type LzlField struct {
	ErrNO  int32                      `json:"errno"`
	ErrMsg string                     `json:"errmsg"`
	Data   map[string]json.RawMessage `json:"data"`
}

type LzlContent struct {
	ThreadID  uint64        `json:"thread_id,string"`
	PostID    uint64        `json:"post_id,string"`
	CommentID uint64        `json:"comment_id,string"`
	UserName  string        `json:"username"`
	Content   template.HTML `json:"content"`
}

type LzlComment struct {
	Num     uint32       `json:"comment_num"`
	ListNum uint32       `json:"comment_list_num"`
	Info    []LzlContent `json:"comment_info"`
	// Info []json.RawMessage `json:"comment_info"`
}

type OutputField struct {
	UserName string
	Content  template.HTML
	PostNO   uint32
	PostID   uint64
}

type TemplateField struct {
	Title string
	Posts chan *OutputField
	Lzls  chan map[uint64]*LzlComment
}
