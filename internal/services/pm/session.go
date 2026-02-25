package pm

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/infra"
)

type SessionTail struct {
	TmuxSocket string
	Session    string
	PaneID     string
	Target     string

	CapturedAt time.Time
	Lines      []string
}

func (s *Service) ManagerSessionName() string {
	p, _, err := s.require()
	if err != nil {
		return "ts-unknown-mgr"
	}
	key := strings.TrimSpace(p.Key)
	if key == "" {
		key = "unknown"
	}
	return "ts-" + key + "-mgr"
}

func (s *Service) EnsureManagerSession(ctx context.Context) (string, error) {
	p, _, err := s.require()
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	socket := strings.TrimSpace(p.Config.WithDefaults().TmuxSocket)
	session := s.ManagerSessionName()

	// 已存在就直接返回
	listCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	sessions, err := p.Tmux.ListSessions(listCtx, socket)
	if err == nil && sessions[session] {
		return session, nil
	}

	// 创建 manager session。
	//
	// v0 目标：用户一 attach 就能看到“可对话的 manager”（优先 claude），如果本机没有安装 claude，
	// 则回退到 bash，并提示如何手动启动。
	cmdLine := strings.TrimSpace(p.Config.WithDefaults().ManagerCommand)
	if cmdLine == "" {
		cmdLine = strings.Join([]string{
			`if command -v claude >/dev/null 2>&1; then`,
			`  exec claude`,
			`fi`,
			`echo "未找到 claude 命令。请先安装 Claude Code，或在此 session 手动启动任意 manager（例如：claude）。"`,
			`exec bash`,
		}, "\n")
	}

	newCtx, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	cwd := strings.TrimSpace(p.RepoRoot)
	if cwd == "" {
		cwd = strings.TrimSpace(p.Layout.ProjectDir)
	}
	if err := p.Tmux.NewSessionWithCommand(newCtx, socket, session, cwd, []string{"bash", "-lc", cmdLine}); err != nil {
		return "", err
	}
	return session, nil
}

func (s *Service) SendManagerLine(ctx context.Context, line string) error {
	p, _, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	session, err := s.EnsureManagerSession(ctx)
	if err != nil {
		return err
	}

	socket := strings.TrimSpace(p.Config.WithDefaults().TmuxSocket)
	if socket == "" {
		socket = "dalek"
	}

	target := session + ":0.0"
	if t, _, err := infra.PickObservationTarget(p.Tmux, ctx, socket, session); err == nil && strings.TrimSpace(t) != "" {
		target = strings.TrimSpace(t)
	}
	return p.Tmux.SendLine(ctx, socket, target, line)
}

func (s *Service) ManagerAttachCmd(ctx context.Context) (*exec.Cmd, error) {
	p, _, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	session, err := s.EnsureManagerSession(ctx)
	if err != nil {
		return nil, err
	}
	return p.Tmux.AttachCmd(p.Config.WithDefaults().TmuxSocket, session), nil
}

func (s *Service) CaptureManagerTail(ctx context.Context, lastLines int) (SessionTail, error) {
	p, _, err := s.require()
	if err != nil {
		return SessionTail{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	session, err := s.EnsureManagerSession(ctx)
	if err != nil {
		return SessionTail{}, err
	}

	socket := strings.TrimSpace(p.Config.WithDefaults().TmuxSocket)
	if socket == "" {
		socket = "dalek"
	}

	target := session + ":0.0"
	paneID := ""
	if t, pinfo, err := infra.PickObservationTarget(p.Tmux, ctx, socket, session); err == nil && strings.TrimSpace(t) != "" {
		target = strings.TrimSpace(t)
		paneID = strings.TrimSpace(pinfo.PaneID)
	}

	if lastLines <= 0 {
		lastLines = 20
	}

	out, err := p.Tmux.CapturePane(ctx, socket, target, lastLines)
	if err != nil {
		return SessionTail{}, err
	}
	lines := infra.SplitLines(out)
	lines = infra.TrimTrailingEmpty(lines)
	if len(lines) > lastLines {
		lines = lines[len(lines)-lastLines:]
	}
	return SessionTail{
		TmuxSocket: socket,
		Session:    session,
		PaneID:     paneID,
		Target:     target,
		CapturedAt: time.Now(),
		Lines:      lines,
	}, nil
}

// CaptureManagerTailPreview 将 manager session 的尾部输出抓取结果转成 TailPreview，
// 便于 UI 复用同一套“输出尾部”面板。
func (s *Service) CaptureManagerTailPreview(ctx context.Context, lastLines int) (contracts.TailPreview, error) {
	t, err := s.CaptureManagerTail(ctx, lastLines)
	if err != nil {
		return contracts.TailPreview{}, err
	}
	return contracts.TailPreview{
		TicketID:    0,
		WorkerID:    0,
		TmuxSocket:  t.TmuxSocket,
		TmuxSession: t.Session,
		PaneID:      t.PaneID,
		Target:      t.Target,
		CapturedAt:  t.CapturedAt,
		Lines:       t.Lines,
	}, nil
}
