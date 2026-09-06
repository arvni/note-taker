package email

import (
	"mime"
	"mime/multipart"
	"strings"
	"testing"
)

func TestBuildMIMEWithAttachments(t *testing.T) {
	raw := buildMIME("from@x.com", "to@y.com", "Subj", "<p>hi</p>", []Attachment{
		{Filename: "summary.txt", ContentType: "text/plain; charset=UTF-8", Data: []byte("hello world")},
	})
	s := string(raw)
	hdr, body, ok := strings.Cut(s, "\r\n\r\n")
	if !ok {
		t.Fatal("no header/body split")
	}
	if !strings.Contains(hdr, "multipart/mixed") {
		t.Fatalf("expected multipart/mixed, got: %s", hdr)
	}
	// Parse boundary and walk the parts.
	_, params, err := mime.ParseMediaType(strings.TrimPrefix(lastHeader(hdr, "Content-Type:"), " "))
	if err != nil {
		t.Fatalf("parse media type: %v", err)
	}
	mr := multipart.NewReader(strings.NewReader(body), params["boundary"])
	var sawHTML, sawAtt bool
	for {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		ct := p.Header.Get("Content-Type")
		if strings.Contains(ct, "text/html") {
			sawHTML = true
		}
		if strings.Contains(p.Header.Get("Content-Disposition"), "summary.txt") {
			sawAtt = true
		}
	}
	if !sawHTML || !sawAtt {
		t.Fatalf("missing part: html=%v att=%v", sawHTML, sawAtt)
	}
}

func TestBuildMIMEPlainNoAttachments(t *testing.T) {
	s := string(buildMIME("a", "b", "c", "<p>x</p>", nil))
	if !strings.Contains(s, "text/html") || strings.Contains(s, "multipart") {
		t.Fatalf("expected plain html, got: %s", s)
	}
}

func lastHeader(hdr, key string) string {
	for _, line := range strings.Split(hdr, "\r\n") {
		if strings.HasPrefix(line, key) {
			return strings.TrimPrefix(line, key)
		}
	}
	return ""
}
