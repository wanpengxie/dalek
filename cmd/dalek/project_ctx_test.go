package main

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestProjectCtx_CanceledOnSIGHUP(t *testing.T) {
	ctx, cancel := projectCtx(0)
	defer cancel()

	// 向自身进程发送 SIGHUP，模拟 sh -c 父 shell 退出。
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatalf("failed to send SIGHUP: %v", err)
	}

	select {
	case <-ctx.Done():
		// 预期：SIGHUP 触发 context cancel。
	case <-time.After(2 * time.Second):
		t.Fatal("context should be canceled after SIGHUP within 2s")
	}
}

func TestProjectCtx_CanceledOnSIGINT(t *testing.T) {
	ctx, cancel := projectCtx(0)
	defer cancel()

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("failed to send SIGINT: %v", err)
	}

	select {
	case <-ctx.Done():
		// 预期：SIGINT 触发 context cancel。
	case <-time.After(2 * time.Second):
		t.Fatal("context should be canceled after SIGINT within 2s")
	}
}

func TestProjectCtx_WithTimeout(t *testing.T) {
	ctx, cancel := projectCtx(50 * time.Millisecond)
	defer cancel()

	select {
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("expected deadline exceeded, got=%v", ctx.Err())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("context should be canceled after timeout within 2s")
	}
}

func TestProjectCtx_ManualCancel(t *testing.T) {
	ctx, cancel := projectCtx(0)

	cancel()

	select {
	case <-ctx.Done():
		// 预期：手动 cancel 触发 context cancel。
	case <-time.After(200 * time.Millisecond):
		t.Fatal("context should be canceled immediately after manual cancel")
	}
}
