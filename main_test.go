package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	larkcard "github.com/larksuite/oapi-sdk-go/v3/card"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
)

func TestCardHandlerDoesNotLogPassword(t *testing.T) {
	const password = "test-password-must-not-appear-in-logs"

	output := captureStdout(t, func() {
		h := newCardActionHandler("", "", func(context.Context, *larkcard.CardAction) (any, error) {
			return nil, nil
		})
		h.Handle(context.Background(), &larkevent.EventReq{
			Body: []byte(`{"action":{"form_value":{"new_password":"` + password + `"}}}`),
		})
	})

	if strings.Contains(output, password) {
		t.Fatalf("card callback log leaked password: %q", output)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	previous := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("create stdout pipe: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = previous
		_ = reader.Close()
		_ = writer.Close()
	}()

	done := make(chan []byte, 1)
	go func() {
		output, _ := io.ReadAll(reader)
		done <- output
	}()

	fn()
	_ = writer.Close()
	return string(<-done)
}
