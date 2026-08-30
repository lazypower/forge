// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package work

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"

	"gitea.dev/modules/json"
)

const (
	workCursorVersion = 1
	issueNumberOrder  = "issue_number_asc"
)

type pageCursor struct {
	Version      int    `json:"v"`
	RepositoryID int64  `json:"r"`
	TopKind      string `json:"t"`
	TopID        int64  `json:"i"`
	ProjectID    int64  `json:"p"`
	PageKind     string `json:"k"`
	Order        string `json:"o"`
	LastIssue    int64  `json:"l"`
	LastRelated  int64  `json:"q,omitempty"`
}

type cursorPosition struct {
	Issue   int64
	Related int64
}

func encodeCursor(secret string, cursor pageCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal Work cursor: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeCursor(secret, encoded string, expected pageCursor) (cursorPosition, error) {
	if encoded == "" {
		return cursorPosition{}, nil
	}
	payloadPart, signaturePart, ok := strings.Cut(encoded, ".")
	if !ok {
		return cursorPosition{}, ErrInvalidCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return cursorPosition{}, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(signaturePart)
	if err != nil {
		return cursorPosition{}, ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursorPosition{}, ErrInvalidCursor
	}
	var actual pageCursor
	if json.Unmarshal(payload, &actual) != nil || actual.Version != workCursorVersion || actual.LastIssue <= 0 {
		return cursorPosition{}, ErrInvalidCursor
	}
	if actual.RepositoryID != expected.RepositoryID || actual.TopKind != expected.TopKind || actual.TopID != expected.TopID ||
		actual.ProjectID != expected.ProjectID || actual.PageKind != expected.PageKind || actual.Order != expected.Order {
		return cursorPosition{}, ErrInvalidCursor
	}
	return cursorPosition{Issue: actual.LastIssue, Related: actual.LastRelated}, nil
}
