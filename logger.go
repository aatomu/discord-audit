package main

import (
	"fmt"
	"log"
	"strings"
)

type LogSystem string

const (
	SystemPreload LogSystem = "Preload"
	SystemUser    LogSystem = "User"
	SystemGuild   LogSystem = "Guild"
	SystemChannel LogSystem = "Channel"
	SystemMessage LogSystem = "Message"
	SystemEmoji   LogSystem = "Emoji"
)

type LogLevel string

const (
	LevelError LogLevel = "ERROR"
	LevelWarn  LogLevel = "WARN"
	LevelInfo  LogLevel = "INFO"
)

// LogCtx はログの階層コンテキストを組み立てるビルダー。
// guild(id) > channel(id) > user(id) > message(id) > emoji(id) の順で連結する。
// 呼び出し側は必要な階層だけ呼べばよい(例: Ctx().Guild(...).Channel(...))。
type LogCtx struct {
	parts []string
}

func Ctx() *LogCtx {
	return &LogCtx{}
}

func (c *LogCtx) Guild(name, id string) *LogCtx {
	c.parts = append(c.parts, fmt.Sprintf("guild:%s(%s)", name, id))
	return c
}

func (c *LogCtx) Channel(name, id string) *LogCtx {
	c.parts = append(c.parts, fmt.Sprintf("channel:%s(%s)", name, id))
	return c
}

func (c *LogCtx) User(name, id string) *LogCtx {
	c.parts = append(c.parts, fmt.Sprintf("user:%s(%s)", name, id))
	return c
}

func (c *LogCtx) Message(id string) *LogCtx {
	c.parts = append(c.parts, fmt.Sprintf("message(%s)", id))
	return c
}

func (c *LogCtx) Emoji(name, id string) *LogCtx {
	c.parts = append(c.parts, fmt.Sprintf("emoji:%s(%s)", name, id))
	return c
}

func (c *LogCtx) String() string {
	return strings.Join(c.parts, " > ")
}

// Log は "timestamp [system/level]: ctx: msg" の統一書式で出力する。
// ctx が nil、または空の場合は ctx 部分を省略する。
func Log(s LogSystem, l LogLevel, ctx *LogCtx, format string, arg ...any) {
	msg := fmt.Sprintf(format, arg...)
	ts := nowUTC().Format("2006-01-02 15:04:05")
	if ctx != nil && len(ctx.parts) > 0 {
		log.Printf("%s [%s/%s]: %s: %s", ts, s, l, ctx.String(), msg)
	} else {
		log.Printf("%s [%s/%s]: %s", ts, s, l, msg)
	}
}
