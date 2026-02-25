package app

import "errors"

var ErrNotInitialized = errors.New("dalek 未初始化（该 repo 尚未注册到 Home；请先运行 `dalek init` 或在 TUI 里添加项目）")
