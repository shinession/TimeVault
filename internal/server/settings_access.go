package server

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"timevault/internal/access"
	"timevault/internal/auth"
)

// The access editor of the folder settings: for each level, who has it. Each list is a set of check boxes of the
// same name, so the form works without scripts; the script only filters long lists and keeps "guest" and "everyone"
// apart from the people (see app.js).

// pickerLevels are the levels the editor offers, lowest first.
var pickerLevels = []access.Level{access.Browse, access.Backup, access.Full}

// summaryLimit is how many people the summary of a folder's access names before it says "and n more".
const summaryLimit = 12

// accessField is the name of the form field of a level.
func accessField(l access.Level) string { return "access_" + l.String() }

// accessSpecOf reads the access editor of a posted form. The form carries the hidden field access_set whenever it
// has an editor, because a list in which nobody is ticked posts no field at all.
func accessSpecOf(r *http.Request) (spec access.Spec, set bool) {
	return access.Spec{
		Browse: r.PostForm[accessField(access.Browse)],
		Backup: r.PostForm[accessField(access.Backup)],
		Full:   r.PostForm[accessField(access.Full)],
	}, r.PostFormValue("access_set") != ""
}

type pickerPerson struct {
	ID string
	// Token is what the check box submits: the employee ID written so that it can never be taken for a group.
	Token   string
	Name    string
	Checked bool
	// Gone marks an account that has access but is not an active account of the directory (any more). It stays in
	// the list, so that saving does not drop it without anybody noticing; unticking it removes it.
	Gone bool
}

type pickerColumn struct {
	Level access.Level
	// Field is the name of the form field of the level.
	Field string
	// Guest and Everyone say that the group is chosen for this level. AllowGuest is false where guests cannot be
	// offered: they can only be given Browse.
	Guest, Everyone, AllowGuest bool
	People                      []pickerPerson
	// Count is how many entries of the level are chosen.
	Count int
}

type accessPicker struct {
	Columns []pickerColumn
	// Listed is false when the user directory could not list its accounts: only the accounts that already have
	// access are shown, and nobody else can be added until it can.
	Listed bool
	// Rev is the digest of the grants the editor shows, as they were when the form was built (empty for a folder
	// that is not made yet). The form sends it back: that is how saving tells that somebody else has changed the
	// grants since.
	Rev string
}

// directoryAccounts lists the accounts people can be picked from; ok is false when the directory cannot say.
func (s *Server) directoryAccounts(ctx context.Context) (accounts []auth.Account, ok bool) {
	accounts, err := s.auth.Accounts(ctx)
	switch {
	case err == nil:
		return accounts, true
	case errors.Is(err, auth.ErrNoAccountList):
	default:
		s.log.Warn("the accounts of the user directory could not be listed for the access editor", "error", err)
	}
	return nil, false
}

// buildPicker makes the editor for the list that is entered (or was stored). The ticked people come first.
func buildPicker(spec access.Spec, accounts []auth.Account, listed bool) accessPicker {
	picker := accessPicker{Listed: listed}
	for _, level := range pickerLevels {
		var tokens []string
		switch level {
		case access.Browse:
			tokens = spec.Browse
		case access.Backup:
			tokens = spec.Backup
		case access.Full:
			tokens = spec.Full
		}
		col := pickerColumn{Level: level, Field: accessField(level), AllowGuest: level == access.Browse}
		chosen := map[string]bool{}
		for _, token := range tokens {
			// What cannot be read was refused when the list was checked; the editor shows the rest.
			switch kind, id, _ := access.ParseToken(token); kind {
			case access.Guest:
				col.Guest = col.AllowGuest
			case access.Everyone:
				col.Everyone = true
			case access.User:
				chosen[id] = true
			}
		}
		var first, rest []pickerPerson
		listedIDs := map[string]bool{}
		for _, a := range accounts {
			listedIDs[a.ID] = true
			person := pickerPerson{ID: a.ID, Token: access.UserPrefix + a.ID, Name: a.Name, Checked: chosen[a.ID]}
			if person.Checked {
				first = append(first, person)
			} else {
				rest = append(rest, person)
			}
		}
		var gone []string
		for id := range chosen {
			if !listedIDs[id] {
				gone = append(gone, id)
			}
		}
		slices.Sort(gone)
		for _, id := range gone {
			first = append(first, pickerPerson{ID: id, Token: access.UserPrefix + id, Checked: true, Gone: true})
		}
		col.Count = len(first)
		col.People = append(first, rest...)
		picker.Columns = append(picker.Columns, col)
	}
	return picker
}

// accessRow is who has one level of a folder, for the summary on the settings page.
type accessRow struct {
	Level           access.Level
	Guest, Everyone bool
	// People are the accounts, "Name (ID)" or just the ID, at most summaryLimit of them; More counts the rest.
	People []string
	More   int
}

// summarize says who may do what with a folder, for those who may change it. Levels nobody has are left out.
func summarize(list access.List, accounts []auth.Account) []accessRow {
	names := make(map[string]string, len(accounts))
	for _, a := range accounts {
		names[a.ID] = a.Name
	}
	var rows []accessRow
	for _, level := range pickerLevels {
		row := accessRow{Level: level}
		var ids []string
		for _, e := range list {
			if e.Level != level {
				continue
			}
			switch e.Kind {
			case access.Guest:
				row.Guest = true
			case access.Everyone:
				row.Everyone = true
			case access.User:
				ids = append(ids, e.Account)
			}
		}
		for i, id := range ids {
			if i == summaryLimit {
				row.More = len(ids) - summaryLimit
				break
			}
			if name := names[id]; name != "" {
				id = name + " (" + id + ")"
			}
			row.People = append(row.People, id)
		}
		if row.Guest || row.Everyone || len(row.People) > 0 {
			rows = append(rows, row)
		}
	}
	return rows
}
