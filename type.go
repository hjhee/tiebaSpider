package main

import (
	"encoding/json"
	"html/template"
)

type TiebaField struct {
	Author struct {
		UserID uint64 `json:"user_id"`
		UserName string `json:"user_name"` // 用户名
		// Props string `json:"props"`
	} `json:"author"`
	Content struct {
		PostID uint64 `json:"post_id"`
		// IsAnonym bool `json:"is_anonym"`
		ForumID uint64 `json:"forum_id"`
		ThreadID uint64 `json:"thread_id"`
		Content string `json:"content"` // 正文内容
		PostNO uint32 `json:"post_no"` // 楼数
		// Type string `json:"type"`
		// CommentNum uint16 `json:"comment_num"`
		// Props string `json:"props"`
		// PostIndex uint64 `json:"post_index"`
		// PbTpoint *uint64 `json:"pb_tpoint"`
	} `json:"content"`
}

type LzlField struct {
	ErrNO int32 `json:"errno"`
	ErrMsg string `json:"errmsg"`
	Data map[string]json.RawMessage `json:"data"`
}

type LzlContent struct {
	ThreadID uint64 `json:"thread_id,string"`
	PostID uint64 `json:"post_id,string"`
	CommentID uint64 `json:"comment_id,string"`
	UserName string `json:"username"`
	Content template.HTML `json:"content"`
}

type LzlComment struct {
	Num uint32 `json:"comment_num"`
	ListNum uint32 `json:"comment_list_num"`
	Info []LzlContent `json:"comment_info"`
	// Info []json.RawMessage `json:"comment_info"`
}

type OutputField struct {
	UserName string
	Content template.HTML
	PostNO uint32
	PostID uint64
}

type TemplateField struct {
	Posts chan *OutputField
	Lzls chan map[uint64]*LzlComment
}