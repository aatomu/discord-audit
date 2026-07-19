package main

import (
	"time"

	"github.com/bwmarrin/discordgo"
)

type HistoryEntry[T any] struct {
	RecordedAt time.Time
	Data       T
}
type HistoryData[T any] struct {
	Start   T
	History []HistoryEntry[T]
}

type User struct {
	ID            string
	Username      string
	GlobalName    string
	DisplayName   string
	Discriminator string
	Avatar        string
	Banner        string
	AccentColor   int
	Status        string
}

type Guild struct {
	ID              string
	Name            string
	Description     string
	Icon            string
	Banner          string
	Splash          string
	DiscoverySplash string
}

type MemberOperation string

const (
	MemberPrevious MemberOperation = "Previous"
	MemberJoin     MemberOperation = "Join"
	MemberLeave    MemberOperation = "Leave"
	MemberUpdate   MemberOperation = "Update"
)

type Member struct {
	Operation                  MemberOperation
	UserID                     string
	Nickname                   string
	Roles                      []string
	JoinedAt                   time.Time
	CommunicationDisabledUntil *time.Time
}

type Channel struct {
	ID       string
	Name     string
	Topic    string
	IsThread bool
}

type MessageOperation string

const (
	MessageOperationCreate = "Create"
	MessageOperationEdit   = "Edit"
	MessageOperationDelete = "Delete"
)

type Attachment struct {
	Filename  string
	URL       string
	LocalPath string
	Size      int64
}

type Message struct {
	Operation       MessageOperation
	ID              string
	AuthorID        string
	Content         string
	Embeds          []discordgo.MessageEmbed
	Attachments     []Attachment
	Timestamp       time.Time
	EditedTimestamp *time.Time
}

type Emoji struct {
	ID       string
	Name     string
	Animated bool
	Image    string
}
