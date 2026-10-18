package app

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"voice_input/internal/i18n"
	"voice_input/internal/search"
	"voice_input/internal/wininput"
)

type Stage string

const (
	Idle      Stage = "idle"
	Recording Stage = "recording"
	ASR       Stage = "asr"
	LLM       Stage = "llm"
	Search    Stage = "search"
	Injecting Stage = "injecting"
	Done      Stage = "done"
	Skip      Stage = "skip"
	Clarify   Stage = "clarify"
	Error     Stage = "error"
	Cancelled Stage = "cancelled"
)

type State struct {
	TaskID        string        `json:"task_id"`
	Intent        Intent        `json:"intent"`
	Turn          uint64        `json:"turn"`
	Question      string        `json:"question"`
	Replying      bool          `json:"replying"`
	Revision      uint64        `json:"revision"`
	Stage         Stage         `json:"stage"`
	Visible       bool          `json:"visible"`
	RawText       string        `json:"raw_text"`
	Text          string        `json:"text"`
	Notice        string        `json:"notice"`
	NoticeMessage *i18n.Message `json:"notice_i18n,omitempty"`
	HideAt        int64         `json:"hide_at"`
	Pinned        bool          `json:"pinned"`
	Mode          string        `json:"mode"`
	Hotkey        string        `json:"hotkey"`
	Level         float64       `json:"level"`
	Sources       []search.Hit  `json:"sources"`
}
type Result struct {
	NoticeMessage *i18n.Message
	Status        string
	Text          string
	Notice        string
	Sources       []search.Hit
}
type Processor interface {
	Transcribe(context.Context) (string, error)
	Complete(context.Context, string) (Result, error)
}
type Starter interface{ Begin(context.Context) error }
type TaskOptions struct {
	Locale      i18n.Locale
	Mode        string
	Hotkey      string
	RecordLimit time.Duration
	Timeout     time.Duration
}
type Factory func(context.Context, string, Intent, func(Stage, float64)) (Processor, TaskOptions, error)
type Controller struct {
	mu               sync.Mutex
	state            State
	factory          Factory
	processor        Processor
	ready            chan struct{}
	options          TaskOptions
	injector         wininput.Injector
	cancel           context.CancelFunc
	ctx              context.Context
	timer            *time.Timer
	recordTimer      *time.Timer
	autoHide         time.Duration
	changes          chan State
	stopped          time.Time
	conversation     Conversation
	hotkey, aiHotkey string
}

func New(processor Processor, injector wininput.Injector, autoHide time.Duration) *Controller {
	return NewWithFactory(func(_ context.Context, _ string, intent Intent, _ func(Stage, float64)) (Processor, TaskOptions, error) {
		return processor, TaskOptions{Locale: i18n.ZhCN, Mode: "live", Timeout: 45 * time.Second}, nil
	}, injector, autoHide)
}
func NewWithFactory(factory Factory, injector wininput.Injector, autoHide time.Duration) *Controller {
	return &Controller{factory: factory, injector: injector, autoHide: autoHide, hotkey: "Ctrl+Alt+R", aiHotkey: "Ctrl+Alt+A", state: State{Stage: Idle, Intent: Dictation, Mode: "live", Hotkey: "Ctrl+Alt+R", Sources: []search.Hit{}}, changes: make(chan State, 1)}
}
func (c *Controller) Changes() <-chan State { return c.changes }
func (c *Controller) Snapshot() State       { c.mu.Lock(); defer c.mu.Unlock(); return c.state }
func (c *Controller) Busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return active(c.state.Stage) || c.state.Stage == Clarify
}
func active(s Stage) bool {
	return s == Recording || s == ASR || s == LLM || s == Search || s == Injecting
}
func (c *Controller) publish() {
	c.state.Revision++
	select {
	case <-c.changes:
	default:
	}
	c.changes <- c.state
}
func (c *Controller) stopTimer() {
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	c.state.HideAt = 0
}
func (c *Controller) stopRecordTimer() {
	if c.recordTimer != nil {
		c.recordTimer.Stop()
		c.recordTimer = nil
	}
}
func (c *Controller) Toggle(intent Intent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !intent.Valid() {
		return
	}
	if (active(c.state.Stage) || c.state.Stage == Clarify) && c.state.Intent != intent {
		c.notice(i18n.M("app.switch_"+string(c.state.Intent), "hotkey", c.state.Hotkey))
		c.publish()
		return
	}
	if c.state.Stage == Recording {
		c.stopRecording()
		return
	}
	if active(c.state.Stage) {
		c.notice(i18n.M("app.busy"))
		c.publish()
		return
	}
	if c.state.Stage == Clarify && c.state.Visible {
		c.startTurn(true)
		return
	}
	mode, hotkey := c.state.Mode, c.hotkey
	if intent == Dialogue {
		hotkey = c.aiHotkey
	}
	c.state = State{TaskID: rand.Text(), Intent: intent, Revision: c.state.Revision, Mode: mode, Hotkey: hotkey, Sources: []search.Hit{}}
	c.conversation = Conversation{}
	c.startTurn(false)
}

// Reply rejects stale task/turn clicks, and distinguishes starting from stopping
// so an old stop click cannot begin answering a newly returned question.
func (c *Controller) Reply(taskID string, turn uint64, stop bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.state.Visible || c.state.Intent != Dialogue || c.state.TaskID != taskID || c.state.Turn != turn {
		return
	}
	if stop {
		if c.state.Replying && c.state.Stage == Recording {
			c.stopRecording()
		}
		return
	}
	if c.state.Stage == Clarify {
		c.startTurn(true)
	}
}

func (c *Controller) startTurn(replying bool) {
	c.stopTimer()
	c.stopRecordTimer()
	if c.cancel != nil {
		c.cancel()
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	if replying {
		c.ctx = context.WithValue(c.ctx, taskLocaleKey{}, c.options.Locale)
	}
	c.stopped = time.Time{}
	c.state.Turn++
	c.state.Stage, c.state.Replying, c.state.Visible, c.state.Level = Recording, replying, true, 0
	c.notice(i18n.M("app.recording_start"))
	if replying {
		c.notice(i18n.M("app.reply_start"))
	}
	c.processor = nil
	c.options = TaskOptions{}
	c.ready = make(chan struct{})
	ready := c.ready
	id, ctx, turn := c.state.TaskID, c.ctx, c.state.Turn
	c.publish()
	go c.begin(ctx, id, ready, turn)
}
func (c *Controller) begin(ctx context.Context, id string, ready chan struct{}, turn uint64) {
	defer close(ready)
	c.mu.Lock()
	valid := c.valid(ctx, id, turn)
	intent := c.state.Intent
	c.mu.Unlock()
	if !valid {
		return
	}
	// There is deliberately no input-target inspection. Processing is independent
	// of focus; the final Unicode input goes to the then-current active position.
	processor, options, err := c.factory(ctx, id, intent, func(stage Stage, level float64) { c.notify(ctx, id, turn, stage, level) })
	c.mu.Lock()
	if !c.valid(ctx, id, turn) {
		c.mu.Unlock()
		return
	}
	options.Locale = TaskLocale(ctx, options.Locale)
	if options.Locale == "" {
		options.Locale = i18n.ZhCN
	}
	c.processor, c.options = processor, options
	if c.options.Timeout <= 0 {
		c.options.Timeout = 90 * time.Second
	}
	c.state.Mode = options.Mode
	if options.Hotkey != "" {
		c.state.Hotkey = options.Hotkey
	}
	if err != nil {
		c.terminalMessage(Error, i18n.FromError(err, "app.init_failed"))
		c.mu.Unlock()
		return
	}
	if c.state.Stage == ASR {
		c.notice(i18n.Message{})
		c.startProcessingContext()
	}
	if c.ctx.Err() != nil {
		c.finishTimeout(c.ctx, id, turn)
		c.mu.Unlock()
		return
	}
	ctx = c.ctx
	if c.state.Stage == Recording {
		c.notice(i18n.M("app.microphone_open"))
	}
	c.publish()
	c.mu.Unlock()
	if starter, ok := processor.(Starter); ok {
		err = starter.Begin(ctx)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid(ctx, id, turn) {
		return
	}
	if err != nil {
		c.recordingFailure(i18n.FromError(err, "app.microphone_failed"))
		return
	}
	if c.state.Stage == Recording {
		c.notice(i18n.Message{})
		c.publish()
		if options.RecordLimit > 0 {
			c.recordTimer = time.AfterFunc(options.RecordLimit, func() {
				c.mu.Lock()
				defer c.mu.Unlock()
				if c.state.TaskID == id && c.state.Turn == turn && c.state.Stage == Recording {
					c.stopRecording()
				}
			})
		}
	}
}
func (c *Controller) stopRecording() {
	c.stopRecordTimer()
	c.stopped = time.Now()
	c.state.Stage, c.state.Level = ASR, 0
	c.notice(i18n.Message{})
	c.publish()
	if c.options.Timeout > 0 {
		c.startProcessingContext()
	}
	go c.process(c.ctx, c.state.TaskID, c.ready, c.state.Turn)
}
func (c *Controller) notify(ctx context.Context, id string, turn uint64, stage Stage, level float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid(ctx, id, turn) {
		return
	}
	if stage == Recording {
		if c.state.Stage != Recording {
			return
		}
		c.state.Level = max(0, min(1, level))
	} else if stage == LLM || stage == Search {
		if c.state.Stage != LLM && c.state.Stage != Search {
			return
		}
		c.state.Stage = stage
		c.notice(i18n.Message{})
	} else {
		return
	}
	c.publish()
}
func (c *Controller) valid(ctx context.Context, id string, turn uint64) bool {
	return ctx.Err() == nil && c.state.TaskID == id && c.state.Turn == turn && active(c.state.Stage)
}
func (c *Controller) terminalMessage(stage Stage, message i18n.Message) {
	c.terminal(stage, "", message)
}

func (c *Controller) terminal(stage Stage, notice string, messages ...i18n.Message) {
	c.stopRecordTimer()
	c.stopTimer()
	c.state.Stage, c.state.Notice, c.state.Level = stage, notice, 0
	c.state.NoticeMessage = nil
	if len(messages) != 0 {
		c.notice(messages[0])
	}
	c.state.Question, c.state.Replying = "", false
	if c.cancel != nil {
		c.cancel()
	}
	if !c.state.Pinned {
		id, turn := c.state.TaskID, c.state.Turn
		c.state.HideAt = time.Now().Add(c.autoHide).UnixMilli()
		c.timer = time.AfterFunc(c.autoHide, func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.state.TaskID != id || c.state.Turn != turn || active(c.state.Stage) || c.state.Pinned {
				return
			}
			c.state.Visible = false
			c.state.HideAt = 0
			c.publish()
		})
	}
	c.publish()
}

// awaitReply releases the completed turn, but keeps the task and question alive.
// Waiting for a person does not consume a processing timeout or a hide timer.
func (c *Controller) awaitReply(question, notice string, messages ...i18n.Message) {
	c.stopRecordTimer()
	c.stopTimer()
	c.state.Stage, c.state.Question, c.state.Notice = Clarify, question, notice
	c.state.NoticeMessage = nil
	if len(messages) != 0 {
		c.notice(messages[0])
	}
	c.state.Replying, c.state.Visible, c.state.Level = false, true, 0
	if c.cancel != nil {
		c.cancel()
	}
	c.publish()
}

func (c *Controller) recordingFailure(message i18n.Message) {
	if c.state.Replying {
		retry := i18n.M(message.Code + ".retry")
		if retry.Text(i18n.EN) != "" {
			message = retry
		}
		c.awaitReply(c.state.Question, "", message)
	} else {
		c.terminalMessage(Error, message)
	}
}

// KeepOpen is task-correlated so a delayed mouse event cannot pin a new task.
func (c *Controller) KeepOpen(taskID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if taskID == "" || taskID != c.state.TaskID || !c.state.Visible || c.state.Pinned {
		return
	}
	c.state.Pinned = true
	c.stopTimer()
	c.publish()
}

func (c *Controller) process(ctx context.Context, id string, ready <-chan struct{}, turn uint64) {
	select {
	case <-ctx.Done():
		return
	case <-ready:
	}
	c.mu.Lock()
	if !c.valid(ctx, id, turn) {
		c.mu.Unlock()
		return
	}
	ctx = c.ctx
	p := c.processor
	if ctx.Err() != nil {
		c.finishTimeout(ctx, id, turn)
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()
	raw, err := p.Transcribe(ctx)
	c.mu.Lock()
	if !c.valid(ctx, id, turn) {
		c.finishTimeout(ctx, id, turn)
		c.mu.Unlock()
		return
	}
	if err != nil {
		c.recordingFailure(i18n.FromError(err, "app.asr_failed"))
		c.mu.Unlock()
		return
	}
	conversation := c.conversation.Clone()
	if c.state.Replying {
		if strings.TrimSpace(raw) == "" || len(raw) > 32000 || !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) {
			c.recordingFailure(i18n.M("app.answer_empty"))
			c.mu.Unlock()
			return
		}
		conversation.Clarifications = append(conversation.Clarifications, Clarification{Question: c.state.Question, Answer: raw})
	} else {
		conversation.Original = raw
	}
	if err := conversation.Validate(); err != nil {
		c.terminalMessage(Error, i18n.FromError(err, "app.conversation_large"))
		c.mu.Unlock()
		return
	}
	c.conversation = conversation
	c.state.RawText, c.state.Stage = conversation.TranscriptForLocale(c.options.Locale), LLM
	c.notice(i18n.Message{})
	c.publish()
	c.mu.Unlock()
	var result Result
	if len(conversation.Clarifications) == 0 {
		result, err = p.Complete(ctx, conversation.Original)
	} else if processor, ok := p.(ConversationProcessor); ok {
		result, err = processor.CompleteConversation(ctx, conversation.Clone())
	} else {
		err = i18n.Error("app.unsupported_reply")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid(ctx, id, turn) {
		c.finishTimeout(ctx, id, turn)
		return
	}
	if err != nil {
		c.terminalMessage(Error, i18n.FromError(err, "app.llm_failed"))
		return
	}
	if err = validate(result); err != nil {
		c.terminalMessage(Error, i18n.M("app.result_invalid"))
		return
	}
	c.state.Text, c.state.Sources = result.Text, result.Sources
	switch result.Status {
	case "skip":
		if result.NoticeMessage != nil {
			c.terminalMessage(Skip, *result.NoticeMessage)
		} else {
			c.terminal(Skip, result.Notice)
		}
	case "clarify":
		if c.state.Intent != Dialogue {
			c.terminalMessage(Error, i18n.M("app.dictation_clarify"))
			return
		}
		if len(c.conversation.Clarifications) >= MaxClarifications {
			c.terminalMessage(Error, i18n.M("app.clarification_limit"))
			return
		}
		c.awaitReply(result.Notice, "")
	case "insert":
		normalized, normalizeErr := wininput.Normalize(result.Text)
		if normalizeErr != nil {
			c.terminalMessage(Error, i18n.FromError(normalizeErr, "app.input_failed"))
			return
		}
		c.state.Text, result.Text = normalized, normalized
		c.state.Stage = Injecting
		c.notice(i18n.Message{})
		c.publish()
		// Preparation can wait for held modifiers. Cancellation remains effective
		// until the short final commit; no foreground or control identity is read.
		c.mu.Unlock()
		commit, err := c.injector.Prepare(ctx, result.Text)
		c.mu.Lock()
		if !c.valid(ctx, id, turn) {
			c.finishTimeout(ctx, id, turn)
			return
		}
		if err == nil && commit != nil {
			err = commit()
		}
		if err != nil || commit == nil {
			c.terminalMessage(Error, i18n.FromError(err, "app.input_failed"))
			return
		}
		c.terminal(Done, "")
	}
}
func validate(r Result) error {
	if strings.ContainsRune(r.Text, 0) || strings.ContainsRune(r.Notice, 0) || !utf8.ValidString(r.Text) || !utf8.ValidString(r.Notice) || len(r.Text) > 32000 || len(r.Notice) > 2000 {
		return errors.New("result too long")
	}
	switch r.Status {
	case "insert":
		if strings.TrimSpace(r.Text) == "" {
			return errors.New("empty insertion")
		}
	case "skip":
		if r.Text != "" {
			return errors.New("skip with text")
		}
	case "clarify":
		if r.Text != "" || strings.TrimSpace(r.Notice) == "" {
			return errors.New("invalid clarification")
		}
	default:
		return errors.New("unknown status")
	}
	return nil
}
func (c *Controller) finishTimeout(ctx context.Context, id string, turn uint64) {
	if c.state.TaskID == id && c.state.Turn == turn && active(c.state.Stage) && ctx.Err() != nil {
		c.terminalMessage(Error, i18n.M("app.timeout"))
	}
}
func (c *Controller) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopTimer()
	c.stopRecordTimer()
	if c.cancel != nil {
		c.cancel()
	}
	if active(c.state.Stage) || c.state.Stage == Clarify {
		c.state.Stage = Cancelled
		c.notice(i18n.M("app.cancelled"))
	}
	c.state.Visible, c.state.Pinned, c.state.Level = false, false, 0
	c.state.Question, c.state.Replying = "", false
	c.conversation = Conversation{}
	c.publish()
}

// Presentation changes idle labels only; active task options remain immutable.
func (c *Controller) Presentation(mode, hotkey, aiHotkey string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !active(c.state.Stage) && c.state.Stage != Clarify {
		c.hotkey, c.aiHotkey = hotkey, aiHotkey
		if c.state.Visible {
			return
		}
		c.state.Mode, c.state.Hotkey = mode, hotkey
		c.state.Intent = Dictation
		c.publish()
	}
}

// The processing deadline includes any unfinished microphone startup.
func (c *Controller) startProcessingContext() {
	if _, started := c.ctx.Deadline(); started {
		return
	}
	parentCancel := c.cancel
	child, stop := context.WithDeadline(c.ctx, c.stopped.Add(c.options.Timeout))
	c.ctx, c.cancel = child, func() { stop(); parentCancel() }
	id, turn := c.state.TaskID, c.state.Turn
	go func() { <-child.Done(); c.mu.Lock(); defer c.mu.Unlock(); c.finishTimeout(child, id, turn) }()
}

// Explicit copy is correlated to the displayed task, including pinned results.
func (c *Controller) CopyText(taskID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if taskID == "" || taskID != c.state.TaskID {
		return "", i18n.Error("app.copy_stale")
	}
	text := c.state.Text
	if text == "" {
		text = c.state.RawText
	}
	if text == "" || strings.ContainsRune(text, 0) {
		return "", i18n.Error("app.copy_empty")
	}
	return text, nil
}
