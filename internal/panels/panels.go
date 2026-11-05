// Package panels builds the dashboard panel of each bot feature. Every
// constructor takes the feature's read-only status behind a small interface
// (so tests use fakes) and returns a dashboard.Panel whose Snapshot is a
// JSON-ready struct. The matching renderer is in
// internal/dashboard/static/app.js under the same ID.
//
// Adding a feature: add a constructor here, register it in
// internal/bot/dashboard.go, and add a renderer in app.js (optional: a panel
// without one is shown as plain JSON).
package panels

// Names turns Discord IDs into names from the bot's gateway cache. A name
// that is not cached is "" (the page then shows the ID); panels never call
// Discord.
type Names interface {
	Guild(id string) string
	Channel(id string) string
	Member(guildID, userID string) string
}

// Ref is an ID with its display name, if known.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// noNames knows no names.
type noNames struct{}

func (noNames) Guild(string) string          { return "" }
func (noNames) Channel(string) string        { return "" }
func (noNames) Member(string, string) string { return "" }

func orNoNames(n Names) Names {
	if n == nil {
		return noNames{}
	}
	return n
}

func guildRef(n Names, id string) Ref { return Ref{ID: id, Name: n.Guild(id)} }

func channelRef(n Names, id string) *Ref {
	if id == "" {
		return nil
	}
	return &Ref{ID: id, Name: n.Channel(id)}
}

func memberRef(n Names, guildID, userID string) Ref {
	return Ref{ID: userID, Name: n.Member(guildID, userID)}
}
