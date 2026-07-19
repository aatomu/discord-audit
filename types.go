package main

import (
	"time"

	"github.com/bwmarrin/discordgo"
)

type HistoryEntry[T any] struct {
	RecordedAt time.Time `json:"recordedAt"`
	Data       T         `json:"data"`
}

type HistoryData[T any] struct {
	Start   T                 `json:"start"`
	History []HistoryEntry[T] `json:"history"`
}

type User struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	GlobalName    string `json:"globalName"`
	DisplayName   string `json:"displayName"`
	Discriminator string `json:"discriminator"`
	Avatar        string `json:"avatar"`
	Banner        string `json:"banner"`
	AccentColor   int    `json:"accentColor"`
	Status        string `json:"status"`
}

type Guild struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Icon            string `json:"icon"`
	Banner          string `json:"banner"`
	Splash          string `json:"splash"`
	DiscoverySplash string `json:"discoverySplash"`
}

type MemberOperation string

const (
	MemberPrevious MemberOperation = "Previous"
	MemberJoin     MemberOperation = "Join"
	MemberLeave    MemberOperation = "Leave"
	MemberUpdate   MemberOperation = "Update"
)

type Member struct {
	Operation                  MemberOperation `json:"operation"`
	UserID                     string          `json:"userId"`
	Nickname                   string          `json:"nickname"`
	Roles                      []string        `json:"roles"`
	JoinedAt                   time.Time       `json:"joinedAt"`
	CommunicationDisabledUntil *time.Time      `json:"communicationDisabledUntil"`
}

type Channel struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Topic    string `json:"topic"`
	IsThread bool   `json:"isThread"`
}

type MessageOperation string

const (
	MessageOperationCreate MessageOperation = "Create"
	MessageOperationEdit   MessageOperation = "Edit"
	MessageOperationDelete MessageOperation = "Delete"
)

type Attachment struct {
	Filename  string `json:"filename"`
	URL       string `json:"url"`
	LocalPath string `json:"localPath"`
	Size      int64  `json:"size"`
}

type Message struct {
	Operation       MessageOperation         `json:"operation"`
	ID              string                   `json:"id"`
	AuthorID        string                   `json:"authorId"`
	Content         string                   `json:"content"`
	Embeds          []discordgo.MessageEmbed `json:"embeds"`
	Attachments     []Attachment             `json:"attachments"`
	Timestamp       time.Time                `json:"timestamp"`
	EditedTimestamp *time.Time               `json:"editedTimestamp"`
}

type Emoji struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Animated bool   `json:"animated"`
	Image    string `json:"image"`
}
