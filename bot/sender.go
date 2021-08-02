package bot

import (
	"fmt"
	"github.com/slack-go/slack"
	"log"
	"strings"
	"time"
)

type Format int

const (
	Text = iota
	Markdown
	Code
)

// TODO deal with rate limiting

type Sender interface {
	Send(message string, format Format) error
	SendWithID(message string, format Format) (string, error)
	Update(id string, message string, format Format) error
}

type SlackSender struct {
	rtm      *slack.RTM
	channel  string
	threadTS string
}

func NewSlackSender(rtm *slack.RTM, channel string, threadTS string) *SlackSender {
	return &SlackSender{
		rtm:      rtm,
		channel:  channel,
		threadTS: threadTS,
	}
}

func (s *SlackSender) Send(message string, format Format) error {
	_, err := s.SendWithID(message, format)
	return err
}

func (s *SlackSender) SendWithID(message string, format Format) (string, error) {
	switch format {
	case Text:
		return s.send(s.formatText(message))
	case Markdown:
		return s.send(s.formatMarkdown(message))
	case Code:
		return s.send(s.formatCode(message))
	default:
		return "", fmt.Errorf("invalid format: %d", format)
	}
}

func (s *SlackSender) Update(id string, message string, format Format) error {
	switch format {
	case Text:
		return s.update(id, s.formatText(message))
	case Markdown:
		return s.update(id, s.formatMarkdown(message))
	case Code:
		return s.update(id, s.formatCode(message))
	default:
		return fmt.Errorf("invalid format: %d", format)
	}
}

func (s *SlackSender) formatText(message string) slack.MsgOption {
	return slack.MsgOptionText(message, false)
}

func (s *SlackSender) formatCode(message string) slack.MsgOption {
	return s.formatMarkdown(fmt.Sprintf("```%s```", strings.ReplaceAll(message, "```", "` ` `"))) // Hack ...
}

func (s *SlackSender) formatMarkdown(markdown string) slack.MsgOption {
	section := slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", markdown, false, true), nil, nil)
	return slack.MsgOptionBlocks(section)
}

func (s *SlackSender) send(msg slack.MsgOption) (string, error) {
	options := []slack.MsgOption{msg}
	if s.threadTS != "" {
		options = append(options, slack.MsgOptionTS(s.threadTS))
	}
	for {
		_, responseTS, err := s.rtm.PostMessage(s.channel, options...)
		if err == nil {
			return responseTS, nil
		}
		if e, ok := err.(*slack.RateLimitedError); ok {
			log.Printf("error: %s; sleeping before re-sending", err.Error())
			time.Sleep(e.RetryAfter + 500*time.Millisecond)
			continue
		}
		return "", err
	}
}

func (s *SlackSender) update(timestamp string, msg slack.MsgOption) error {
	options := []slack.MsgOption{msg}
	if s.threadTS != "" {
		options = append(options, slack.MsgOptionTS(s.threadTS))
	}
	for {
		_, _, _, err := s.rtm.UpdateMessage(s.channel, timestamp, options...)
		if err == nil {
			return nil
		}
		if e, ok := err.(*slack.RateLimitedError); ok {
			log.Printf("error: %s; sleeping before re-sending", err.Error())
			time.Sleep(e.RetryAfter + 500*time.Millisecond)
			continue
		}
		return err
	}
}
