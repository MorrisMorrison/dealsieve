// Package judge is where an LLM's opinion on a deal will plug in. v1 ships
// only Noop; see the spec's "Later" section.
package judge

import (
	"context"

	"github.com/MorrisMorrison/dealsieve/internal/rules"
)

type Opinion struct {
	Block  bool
	Reason string
}

type Judge interface {
	Judge(ctx context.Context, d rules.Deal) (Opinion, error)
}

type Noop struct{}

func (Noop) Judge(context.Context, rules.Deal) (Opinion, error) { return Opinion{}, nil }
