package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	bridgev1 "github.com/orchael/bridgectl/gen/bridge/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var errSessionExit = errors.New("session exit event received")

type attachSignalHandling int

const (
	attachSignalCancel attachSignalHandling = iota
	attachSignalIgnore
	attachSignalResize
)

func attachSignalAction(sig os.Signal, isWriter bool) attachSignalHandling {
	if !isSigwinch(sig) {
		return attachSignalCancel
	}
	if isWriter {
		return attachSignalResize
	}
	return attachSignalIgnore
}

func handleAttachSignals(ctx context.Context, sigCh <-chan os.Signal, isWriter bool, resize func(), cancel func()) {
	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-sigCh:
			switch attachSignalAction(sig, isWriter) {
			case attachSignalResize:
				resize()
			case attachSignalIgnore:
				continue
			case attachSignalCancel:
				cancel()
				return
			}
		}
	}
}

func sessionExitMessage(ev *bridgev1.AttachSessionEvent) string {
	if ev.GetExitRecorded() {
		return fmt.Sprintf("Session exited with code %d.", ev.GetExitCode())
	}
	return "Session exited."
}

func isCanceledStreamError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return err != nil
	}
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.Canceled
}
