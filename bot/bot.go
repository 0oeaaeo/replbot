package bot

import (
	"context"
	"errors"
	"fmt"
	"github.com/slack-go/slack"
	"golang.org/x/sync/errgroup"
	"heckel.io/replbot/config"
	"log"
	"strings"
	"sync"
	"time"
)

type Bot struct {
	config   *config.Config
	userID   string
	sessions map[string]*session
	ctx      context.Context
	cancelFn context.CancelFunc
	rtm      *slack.RTM
	mu       sync.RWMutex
}

func New(config *config.Config) (*Bot, error) {
	return &Bot{
		config:   config,
		sessions: make(map[string]*session),
	}, nil
}

func (b *Bot) Start() error {
	b.rtm = slack.New(b.config.Token).NewRTM()
	go b.rtm.ManageConnection()

	var g *errgroup.Group
	b.ctx, b.cancelFn = context.WithCancel(context.Background())
	g, b.ctx = errgroup.WithContext(b.ctx)
	g.Go(b.handleIncomingEvents)
	g.Go(b.manageSessions)
	if err := g.Wait(); err != nil && err != errExit {
		return err
	}
	return nil
}

func (b *Bot) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for sessionID, session := range b.sessions {
		log.Printf("[session %s] Force-closing session", sessionID)
		session.CloseWithMessage()
		delete(b.sessions, sessionID)
	}
	b.cancelFn() // This must be at the end, see app.go
}

func (b *Bot) handleIncomingEvents() error {
	for {
		select {
		case <-b.ctx.Done():
			return errExit
		case msg := <-b.rtm.IncomingEvents:
			switch ev := msg.Data.(type) {
			case *slack.ConnectedEvent:
				if ev.Info == nil || ev.Info.User == nil || ev.Info.User.ID == "" {
					return errors.New("missing user info in connected event")
				}
				b.mu.Lock()
				b.userID = ev.Info.User.ID
				b.mu.Unlock()
				log.Printf("Slack connected as user %s/%s", ev.Info.User.Name, ev.Info.User.ID)
			case *slack.LatencyReport:
				log.Printf("Current latency: %v\n", ev.Value)
			case *slack.RTMError:
				log.Printf("Error: %s\n", ev.Error())
			case *slack.ConnectionErrorEvent:
				log.Printf("Error: %s\n", ev.Error())
			case *slack.InvalidAuthEvent:
				return errors.New("invalid credentials")
			case *slack.MessageEvent:
				b.dispatchMessage(ev)
			default:
				// Ignore other events
			}
		}
	}
}

func (b *Bot) dispatchMessage(ev *slack.MessageEvent) {
	if ev.User == "" {
		return // Ignore my own messages
	}
	if strings.HasPrefix(ev.Channel, "D") {
		sessionID := ev.Channel
		if !b.maybeForwardMessage(sessionID, ev.Text) {
			b.startSession(sessionID, ev.Channel, "")
		}
	} else if strings.HasPrefix(ev.Channel, "C") {
		if ev.ThreadTimestamp == "" && b.mentioned(ev.Text) {
			sessionID := fmt.Sprintf("%s:%s", ev.Channel, ev.Timestamp)
			b.startSession(sessionID, ev.Channel, ev.Timestamp)
		} else if ev.ThreadTimestamp != "" {
			sessionID := fmt.Sprintf("%s:%s", ev.Channel, ev.ThreadTimestamp)
			if !b.maybeForwardMessage(sessionID, ev.Text) && b.mentioned(ev.Text) {
				b.startSession(sessionID, ev.Channel, ev.ThreadTimestamp)
			}
		}
	}
}

func (b *Bot) startSession(sessionID string, channel string, threadTS string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sender := NewSlackSender(b.rtm, channel, threadTS)
	b.sessions[sessionID] = NewSession(b.config, sessionID, sender)
	log.Printf("[session %s] Starting new session\n", sessionID)
}

func (b *Bot) maybeForwardMessage(sessionID string, message string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if session, ok := b.sessions[sessionID]; ok && session.Active() {
		_ = session.Send(message) // TODO deal with URLs
		return true
	}
	return false
}

func (b *Bot) manageSessions() error {
	for {
		select {
		case <-b.ctx.Done():
			return errExit
		case <-time.After(15 * time.Second):
		}
		b.mu.Lock()
		for id, session := range b.sessions {
			if !session.Active() {
				log.Printf("[session %s] Removing stale session", session.ID)
				delete(b.sessions, id)
			}
		}
		b.mu.Unlock()
	}
}

func (b *Bot) mentioned(message string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return strings.Contains(message, fmt.Sprintf("<@%s>", b.userID))
}
