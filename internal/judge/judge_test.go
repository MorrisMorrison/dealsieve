package judge

import (
	"errors"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	long := strings.Repeat("x", 300)
	tests := []struct {
		name, in string
		want     Opinion
		wantErr  bool
	}{
		{"plain", `{"rating":"good","reason":"Lowest price this year."}`, Opinion{Good, "Lowest price this year."}, false},
		{"fenced", "```json\n{\"rating\":\"bad\",\"reason\":\"Fees add €70.\"}\n```", Opinion{Bad, "Fees add €70."}, false},
		{"prose around", `Sure! {"rating":"Meh","reason":"Fine."} Hope this helps.`, Opinion{Meh, "Fine."}, false},
		{"newlines collapse", "{\"rating\":\"meh\",\"reason\":\"one\\n\\ttwo   three\"}", Opinion{Meh, "one two three"}, false},
		{"long reason cut", `{"rating":"good","reason":"` + long + `"}`, Opinion{Good, strings.Repeat("x", MaxReason-1) + "…"}, false},
		{"unknown rating", `{"rating":"great","reason":"x"}`, Opinion{}, true},
		{"empty reason", `{"rating":"good","reason":"  "}`, Opinion{}, true},
		{"no json", `I think it's fine.`, Opinion{}, true},
		{"empty", ``, Opinion{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrBadReply) {
					t.Fatalf("err = %v, want ErrBadReply", err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("Parse = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestPrompt(t *testing.T) {
	temp := 120
	d := Deal{Title: "Example Air Flash Sale: Flüge ab 19,99€", Merchant: "Example Air", Price: "19,99€", Category: "Reisen", Temperature: &temp}
	msgs := Prompt(d, "")
	if len(msgs) != 2 || msgs[0].Role != "system" || msgs[1].Role != "user" {
		t.Fatalf("roles = %+v", msgs)
	}
	for _, want := range []string{d.Title, "Example Air", "19,99€", "Reisen", "120°"} {
		if !strings.Contains(msgs[1].Content, want) {
			t.Errorf("user message lacks %q:\n%s", want, msgs[1].Content)
		}
	}
	if strings.Contains(msgs[0].Content, "The user adds:") {
		t.Error("no instructions given, but the system message has the user block")
	}
	withInstr := Prompt(d, "I fly from Berlin. Answer in German.")
	if !strings.Contains(withInstr[0].Content, "The user adds:\nI fly from Berlin. Answer in German.") {
		t.Errorf("instructions missing:\n%s", withInstr[0].Content)
	}
	noTemp := Prompt(Deal{Title: "x"}, "")
	if !strings.Contains(noTemp[1].Content, "Temperature: unknown") {
		t.Errorf("unknown temperature not shown:\n%s", noTemp[1].Content)
	}
}
