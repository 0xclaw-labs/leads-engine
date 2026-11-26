package receptionguard

import (
	"regexp"
	"strings"
)

// ProjectionConsumer identifies the downstream presentation consumer.
type ProjectionConsumer string

const (
	ConsumerDigitalHuman ProjectionConsumer = "digital_human"
	ConsumerCloudBot     ProjectionConsumer = "cloud_bot"
)

// ProjectionSourceReply is an internal reply candidate passed into BuildPresentationProjection.
type ProjectionSourceReply struct {
	ReplyID        string
	TenantID       string
	SessionID      string
	Epoch          int
	SessionVersion int
	Stage          ReplyStage
	LegacyStatus   string // optional interop: "approved", "sent", "generated", "blocked", "superseded"
	Text           string
	Source         OutboundSource
	HumanAuthored  bool
}

// ProjectionSourceEvent is an internal session lifecycle event.
type ProjectionSourceEvent struct {
	Type  string
	Epoch int
}

// ProjectionInput carries full internal context. Sensitive fields (raw PII,
// full conversation transcripts, raw knowledge docs, and long-lived tokens)
// are accepted solely so BuildPresentationProjection can guarantee they are
// stripped and never emitted on the narrow presentation surface.
type ProjectionInput struct {
	Consumer         ProjectionConsumer
	TenantID         string
	SessionID        string
	Mode             Mode
	Epoch            int
	Version          int
	Language         string
	Closed           bool
	Replies          []ProjectionSourceReply
	Events           []ProjectionSourceEvent
	RawPhone         string
	RawWeChat        string
	RawEmail         string
	VisitorKey       string
	LongLivedToken   string
	FullTranscript   []string
	RawKnowledgeDocs []string
}

// DeliverableChannels declares whether text and audio may be emitted.
type DeliverableChannels struct {
	Text  bool `json:"text"`
	Audio bool `json:"audio"`
}

// ProjectedReply is the narrow reply payload allowed for Digital Human (HUI-20) and Cloud Bot.
type ProjectedReply struct {
	ReplyID     string              `json:"reply_id"`
	Epoch       int                 `json:"epoch"`
	Version     int                 `json:"version,omitempty"`
	Text        string              `json:"text"`
	Language    string              `json:"language"`
	Deliverable DeliverableChannels `json:"deliverable"`
}

// ProjectedEvent is a narrow control event (interruption/takeover/handback/etc.).
type ProjectedEvent struct {
	Type  string `json:"type"`
	Epoch int    `json:"epoch"`
}

// PresentationProjection is the strict narrow projection for Digital Human & Cloud Bot.
// It explicitly omits raw PII, full conversation transcripts, raw knowledge docs,
// and long-lived tokens.
type PresentationProjection struct {
	SessionRef string           `json:"session_ref"`
	Mode       Mode             `json:"mode"`
	Epoch      int              `json:"epoch"`
	Version    int              `json:"version,omitempty"`
	Language   string           `json:"language"`
	Replies    []ProjectedReply `json:"replies"`
	Events     []ProjectedEvent `json:"events"`
}

var (
	reCNPhone        = regexp.MustCompile(`(?:\+?86[-\s]?)?1[3-9]\d{9}`)
	reEmail          = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	reWXID           = regexp.MustCompile(`(?i)\bwxid_[A-Za-z0-9_-]{4,}\b`)
	reWeChatLabel    = regexp.MustCompile(`(?i)(?:微信号|微信|wechat|vx)\s*[:：]\s*[A-Za-z0-9_-]{4,32}`)
	reBearerToken    = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._\-]{10,}`)
	reJWTToken       = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
	rePrefixedSecret = regexp.MustCompile(`\b(?:sk|tok|pat|sess|internal_tok|apikey)_[A-Za-z0-9_-]{8,}\b`)
)

// ScrubSensitiveText removes raw phone numbers, WeChat IDs, emails, and
// long-lived tokens as well as any explicitly supplied known secrets.
func ScrubSensitiveText(text string, knownSecrets ...string) string {
	out := text
	for _, secret := range knownSecrets {
		s := strings.TrimSpace(secret)
		if len(s) >= 4 {
			out = strings.ReplaceAll(out, s, "[REDACTED]")
		}
	}
	out = reBearerToken.ReplaceAllString(out, "[REDACTED_TOKEN]")
	out = reJWTToken.ReplaceAllString(out, "[REDACTED_TOKEN]")
	out = rePrefixedSecret.ReplaceAllString(out, "[REDACTED_TOKEN]")
	out = reEmail.ReplaceAllString(out, "[REDACTED_EMAIL]")
	out = reCNPhone.ReplaceAllString(out, "[REDACTED_PHONE]")
	out = reWXID.ReplaceAllString(out, "[REDACTED_WECHAT]")
	out = reWeChatLabel.ReplaceAllString(out, "[REDACTED_WECHAT]")
	return out
}

// BuildPresentationProjection constructs the narrow projection for Digital Human
// (HUI-20) and Cloud Bot consumers.
//
// Guarantees:
//   - Only approved, current-epoch, same-tenant, same-session replies on an open
//     session are projected with Deliverable{Text: true, Audio: true}.
//   - In human_takeover mode, non-human AI/bot replies are excluded.
//   - Only narrow control events (interruption, takeover, handback, release, assist, close) are projected.
//   - Raw PII (phone/wechat/email), visitor keys, full conversation transcripts,
//     raw knowledge docs, and long-lived tokens are stripped and never emitted.
func BuildPresentationProjection(in ProjectionInput) PresentationProjection {
	mode, ok := NormalizeMode(string(in.Mode))
	if !ok {
		mode = ModeAI
	}
	lang := strings.TrimSpace(in.Language)
	if lang == "" {
		lang = "zh"
	}

	out := PresentationProjection{
		SessionRef: strings.TrimSpace(in.SessionID),
		Mode:       mode,
		Epoch:      in.Epoch,
		Version:    in.Version,
		Language:   lang,
		Replies:    []ProjectedReply{},
		Events:     []ProjectedEvent{},
	}

	// Explicitly discard full transcript & raw knowledge docs.
	_ = in.FullTranscript
	_ = in.RawKnowledgeDocs

	secrets := []string{
		in.RawPhone,
		in.RawWeChat,
		in.RawEmail,
		in.VisitorKey,
		in.LongLivedToken,
	}

	if !in.Closed {
		for _, rp := range in.Replies {
			if !isReplyProjectable(in, mode, rp) {
				continue
			}
			cleanText := ScrubSensitiveText(rp.Text, secrets...)
			out.Replies = append(out.Replies, ProjectedReply{
				ReplyID:  strings.TrimSpace(rp.ReplyID),
				Epoch:    rp.Epoch,
				Version:  rp.SessionVersion,
				Text:     cleanText,
				Language: lang,
				Deliverable: DeliverableChannels{
					Text:  true,
					Audio: true,
				},
			})
		}
	}

	for _, ev := range in.Events {
		typ := normalizeProjectionEvent(ev.Type)
		if typ == "" {
			continue
		}
		out.Events = append(out.Events, ProjectedEvent{
			Type:  typ,
			Epoch: ev.Epoch,
		})
	}

	return out
}

func isReplyProjectable(in ProjectionInput, mode Mode, rp ProjectionSourceReply) bool {
	if strings.TrimSpace(rp.ReplyID) == "" {
		return false
	}
	if in.TenantID != "" && rp.TenantID != "" && strings.TrimSpace(rp.TenantID) != strings.TrimSpace(in.TenantID) {
		return false
	}
	if in.SessionID != "" && rp.SessionID != "" && strings.TrimSpace(rp.SessionID) != strings.TrimSpace(in.SessionID) {
		return false
	}
	if rp.Epoch != in.Epoch {
		return false
	}
	approved := rp.Stage == StageApproved || (rp.Stage == "" && strings.EqualFold(strings.TrimSpace(rp.LegacyStatus), "approved"))
	if !approved {
		return false
	}
	if mode == ModeHumanTakeover {
		if rp.Source != "" && rp.Source != SourceHuman && !(rp.Source == SourceDigitalHuman && rp.HumanAuthored) {
			return false
		}
	}
	return true
}

func normalizeProjectionEvent(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "interruption", "takeover", "handback", "release", "assist", "close":
		return strings.TrimSpace(strings.ToLower(raw))
	default:
		return ""
	}
}
