package discord

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/Tulipskun/ai/sdk"
)

type discordMessage struct {
	ID          string
	ChannelID   string
	AuthorID    string
	AuthorName  string
	Content     string
	AuthorIsBot bool
}

func normalizeMessage(message discordMessage) (InputMessage, bool) {
	if message.AuthorIsBot || message.ID == "" || message.ChannelID == "" || message.AuthorID == "" {
		return InputMessage{}, false
	}
	return InputMessage{
		SessionID:  "discord:channel:" + message.ChannelID,
		ChannelID:  message.ChannelID,
		MessageID:  message.ID,
		AuthorID:   message.AuthorID,
		AuthorName: message.AuthorName,
		Content:    message.Content,
	}, true
}

func gatewayIntents() discordgo.Intent {
	return discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentsMessageContent
}

// Gateway is the concrete Discord transport. It owns Discord-specific client
// state and exposes only canonical SDK input/output at its boundary.
type Gateway struct {
	session  *discordgo.Session
	messages chan InputMessage
	done     chan struct{}
	closeMu  sync.Mutex
	closed   bool
}

func NewGateway(token string) (*Gateway, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("discord: bot token is required")
	}
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, err
	}
	gateway := &Gateway{
		session:  session,
		messages: make(chan InputMessage, 256),
		done:     make(chan struct{}),
	}
	session.Identify.Intents = gatewayIntents()
	session.AddHandler(func(_ *discordgo.Session, event *discordgo.MessageCreate) {
		if event == nil || event.Author == nil {
			return
		}
		message, ok := normalizeMessage(discordMessage{
			ID:          event.ID,
			ChannelID:   event.ChannelID,
			AuthorID:    event.Author.ID,
			AuthorName:  event.Author.Username,
			Content:     event.Content,
			AuthorIsBot: event.Author.Bot,
		})
		if !ok {
			return
		}
		select {
		case gateway.messages <- message:
		case <-gateway.done:
		}
	})
	return gateway, nil
}

func (g *Gateway) Start(ctx context.Context) error {
	if g == nil || g.session == nil {
		return errors.New("discord: gateway is not initialized")
	}
	if err := g.session.Open(); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = g.Close(context.Background())
	}()
	return nil
}

func (g *Gateway) Receive(ctx context.Context) (<-chan sdk.Input, error) {
	if g == nil || g.messages == nil {
		return nil, errors.New("discord: gateway is not initialized")
	}
	return InputSource{Messages: g.messages}.Receive(ctx)
}

func (g *Gateway) SendMessage(ctx context.Context, channelID, content string) error {
	if g == nil || g.session == nil {
		return errors.New("discord: gateway is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if channelID == "" || content == "" {
		return errors.New("discord: channel ID and content are required")
	}
	_, err := g.session.ChannelMessageSend(channelID, content)
	return err
}

func (g *Gateway) Display(ctx context.Context, output sdk.Output) error {
	return (Display{Sender: g}).Display(ctx, output)
}

func (g *Gateway) Source() string { return "discord" }

func (g *Gateway) Close(_ context.Context) error {
	if g == nil || g.session == nil {
		return nil
	}
	g.closeMu.Lock()
	if g.closed {
		g.closeMu.Unlock()
		return nil
	}
	g.closed = true
	close(g.done)
	g.closeMu.Unlock()
	return g.session.Close()
}
