package contracts

import (
	"fmt"
	"strings"
)

type InboxReplyAction string

const (
	InboxReplyNone     InboxReplyAction = ""
	InboxReplyContinue InboxReplyAction = "continue"
	InboxReplyDone     InboxReplyAction = "done"
)

func ParseInboxReplyAction(raw string) (InboxReplyAction, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(InboxReplyContinue):
		return InboxReplyContinue, nil
	case string(InboxReplyDone):
		return InboxReplyDone, nil
	default:
		return InboxReplyNone, fmt.Errorf("reply action 非法: %s", strings.TrimSpace(raw))
	}
}
