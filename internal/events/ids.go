package events

import (
	"errors"
	"strconv"
	"strings"
)

// CustomIDPrefix starts the custom ID of every button the events feature
// sends, so the bot can route clicks here.
const CustomIDPrefix = "event:"

// Button kinds.
const (
	kindPick    = "pick"    // event:pick:<picker>:<event>:<1-5>
	kindMore    = "more"    // event:more:<picker>:<event>
	kindRecover = "recover" // event:recover:<event> (🔄 on the create reply)
	kindCancel  = "cancel"  // event:cancel:<event>
	kindKeep    = "keep"    // event:keep:<event>
)

// pickerTokenLen is the length of a picker's hex token.
const pickerTokenLen = 8

var errBadCustomID = errors.New("events: invalid button")

type customID struct {
	kind    string
	picker  string
	eventID string
	choice  int // 1-based, kindPick only
}

func pickID(picker, eventID string, n int) string {
	return CustomIDPrefix + kindPick + ":" + picker + ":" + eventID + ":" + strconv.Itoa(n)
}

func moreID(picker, eventID string) string {
	return CustomIDPrefix + kindMore + ":" + picker + ":" + eventID
}

func recoverID(eventID string) string { return CustomIDPrefix + kindRecover + ":" + eventID }
func cancelID(eventID string) string  { return CustomIDPrefix + kindCancel + ":" + eventID }
func keepID(eventID string) string    { return CustomIDPrefix + kindKeep + ":" + eventID }

// parseCustomID checks a button's custom ID strictly: anyone can send a
// component interaction with any custom ID for a message they can see.
func parseCustomID(s string) (customID, error) {
	if len(s) > 100 || !strings.HasPrefix(s, CustomIDPrefix) {
		return customID{}, errBadCustomID
	}
	parts := strings.Split(strings.TrimPrefix(s, CustomIDPrefix), ":")
	c := customID{kind: parts[0]}
	switch {
	case c.kind == kindPick && len(parts) == 4:
		n, err := strconv.Atoi(parts[3])
		if err != nil || n < 1 || n > pickerSize || parts[3] != strconv.Itoa(n) {
			return customID{}, errBadCustomID
		}
		c.picker, c.eventID, c.choice = parts[1], parts[2], n
	case c.kind == kindMore && len(parts) == 3:
		c.picker, c.eventID = parts[1], parts[2]
	case (c.kind == kindRecover || c.kind == kindCancel || c.kind == kindKeep) && len(parts) == 2:
		c.eventID = parts[1]
	default:
		return customID{}, errBadCustomID
	}
	if !isSnowflake(c.eventID) {
		return customID{}, errBadCustomID
	}
	if (c.kind == kindPick || c.kind == kindMore) && !isToken(c.picker) {
		return customID{}, errBadCustomID
	}
	return c, nil
}

func isSnowflake(s string) bool {
	if s == "" || len(s) > 20 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isToken(s string) bool {
	if len(s) != pickerTokenLen {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
