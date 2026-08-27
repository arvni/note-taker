package directory

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// ParseCSV reads the admin-uploaded employee list fallback (spec §3). The
// required header is `email,name,department,status` (order-independent, case-
// insensitive). Only official-directory-shaped fields are accepted; no
// passwords. Invalid rows are collected and returned alongside valid users.
func ParseCSV(r io.Reader) (users []User, rowErrors []error, err error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1 // tolerate ragged rows; validate per-row below

	header, err := cr.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("csv: read header: %w", err)
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := idx["email"]; !ok {
		return nil, nil, fmt.Errorf("csv: missing required 'email' column (want email,name,department,status)")
	}

	get := func(rec []string, key string) string {
		if i, ok := idx[key]; ok && i < len(rec) {
			return rec[i]
		}
		return ""
	}

	line := 1
	for {
		rec, readErr := cr.Read()
		if readErr == io.EOF {
			break
		}
		line++
		if readErr != nil {
			rowErrors = append(rowErrors, fmt.Errorf("csv line %d: %w", line, readErr))
			continue
		}
		u := User{
			Email:      get(rec, "email"),
			Name:       get(rec, "name"),
			Department: get(rec, "department"),
			Status:     Status(get(rec, "status")),
		}.Normalize()
		if !u.Valid() {
			rowErrors = append(rowErrors, fmt.Errorf("csv line %d: invalid email %q", line, u.Email))
			continue
		}
		users = append(users, u)
	}
	return users, rowErrors, nil
}
