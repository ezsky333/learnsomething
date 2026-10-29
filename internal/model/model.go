package model

// Item 是一道面试题（或一个可勾选的最小学习单元）。
type Item struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	HTML      string `json:"html"`
	Text      string `json:"text"`
	CharCount int    `json:"charCount"`
}

// Section 是一级分组，例如「概念」「数据类型」。
type Section struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Intro string `json:"intro"`
	Items []Item `json:"items"`
}

// DocMeta 是机读的索引信息，体积小，可整体下发。
type DocMeta struct {
	Slug          string `json:"slug"`
	Title         string `json:"title"`
	Group         string `json:"group"`
	Order         int    `json:"order"`
	SourceURL     string `json:"sourceUrl"`
	SourceUpdated string `json:"sourceUpdated"`
	SectionCount  int    `json:"sectionCount"`
	QuestionCount int    `json:"questionCount"`
	CharCount     int    `json:"charCount"`
	// Library 区分内容来源：interview（小林coding 八股）或 algo（代码随想录算法）。
	Library string `json:"library"`
}

// Doc 是整篇学习文档。
type Doc struct {
	DocMeta
	Intro    string    `json:"intro"`
	Sections []Section `json:"sections"`
}

// Manifest 是整个内容库的索引。
type Manifest struct {
	GeneratedAt string    `json:"generatedAt"`
	Docs        []DocMeta `json:"docs"`
}
