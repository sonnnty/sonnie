package events

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// inputError is a rejected /event input. Error is what the user sees;
// field and value also go to the log (see reject).
type inputError struct {
	field string // option or form field, e.g. "when"
	value string // what was typed
	err   error
	hint  string // an extra line for the user, or ""
}

func (e *inputError) Error() string {
	if e.hint == "" {
		return e.err.Error()
	}
	return e.err.Error() + "\n" + e.hint
}

func (e *inputError) Unwrap() error { return e.err }

func badInput(field, value string, err error) *inputError {
	return &inputError{field: field, value: value, err: err}
}

// reMention finds mentions, which Discord inserts when people type @ or #
// in a text option.
var reMention = regexp.MustCompile(`<(?:@[!&]?|#)\d+>|@everyone|@here`)

// timeError is a rejected start or end time, with a hint when the text
// holds mentions (people have typed @someone into when).
func timeError(field, what, value string, err error) *inputError {
	e := badInput(field, value, fmt.Errorf("%s: %w", what, err))
	if reMention.MatchString(value) {
		e.hint = "💡 ช่อง " + field + " ใส่เวลาเท่านั้น — จะแท็กใครให้ใส่ใน description"
	}
	return e
}

// maxLoggedValue and maxLoggedError bound the log line; parser errors
// can quote the whole input.
const (
	maxLoggedValue = 80
	maxLoggedError = 200
)

// reject answers a rejected input privately and logs one line, so the
// owner can see from the logs what was typed and why it failed.
func (h *Handler) reject(i *discordgo.InteractionCreate, who caller, sub string, err error) {
	field, value := "-", ""
	var ie *inputError
	if errors.As(err, &ie) {
		field, value = ie.field, ie.value
	}
	log.Printf("events: rejected /event %s input from user %s: field %s, value %q: %s",
		sub, who.userID, field, truncate(value, maxLoggedValue), truncate(strings.ReplaceAll(err.Error(), "\n", " "), maxLoggedError))
	h.ephemeral(i, err.Error())
}
