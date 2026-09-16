package tui

import (
	"strconv"
	"strings"
	"unicode"
)

type KeyActionType string

const (
	ActionChar      KeyActionType = "char"
	ActionUp        KeyActionType = "up"
	ActionDown      KeyActionType = "down"
	ActionPageUp    KeyActionType = "pageup"
	ActionPageDown  KeyActionType = "pagedown"
	ActionHome      KeyActionType = "home"
	ActionEnd       KeyActionType = "end"
	ActionClick     KeyActionType = "click"
	ActionWheelUp   KeyActionType = "wheel-up"
	ActionWheelDown KeyActionType = "wheel-down"
	ActionCtrlC     KeyActionType = "ctrl-c"
	ActionIgnore    KeyActionType = "ignore"
)

type KeyAction struct {
	Type KeyActionType
	Key  string
	X    int
	Y    int
}

type KeyParser struct {
	pending string
}

func NewKeyParser() *KeyParser {
	return &KeyParser{}
}

func (kp *KeyParser) Feed(chunk string) []KeyAction {
	if len(kp.pending) > 64 {
		kp.pending = ""
	}
	buf := kp.pending + chunk
	var actions []KeyAction
	i := 0

	for i < len(buf) {
		ch := buf[i]

		if ch == '\x1b' {
			if i+1 >= len(buf) {
				break // lone ESC at chunk end - wait
			}
			if buf[i+1] == '[' {
				j := i + 2
				for j < len(buf) && !isFinalCSI(rune(buf[j])) {
					j++
				}
				if j >= len(buf) {
					break // incomplete CSI - wait
				}
				final := buf[j]
				params := buf[i+2 : j]
				actions = append(actions, csiAction(string(final), params))
				i = j + 1
				continue
			}
			if buf[i+1] == 'O' {
				if i+2 >= len(buf) {
					break // incomplete SS3 - wait
				}
				actions = append(actions, csiAction(string(buf[i+2]), ""))
				i += 3
				continue
			}
			// Alt+key or two-byte ESC sequence
			actions = append(actions, KeyAction{Type: ActionIgnore})
			i += 2
			continue
		}

		if ch == '\x03' {
			actions = append(actions, KeyAction{Type: ActionCtrlC})
			i++
			continue
		}

		if ch < ' ' || ch == '\x7f' {
			actions = append(actions, KeyAction{Type: ActionIgnore})
			i++
			continue
		}

		actions = append(actions, KeyAction{Type: ActionChar, Key: string(ch)})
		i++
	}

	kp.pending = buf[i:]
	return actions
}

func isFinalCSI(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~' || r == '@' || r == '`'
}

func csiAction(final string, params string) KeyAction {
	if strings.HasPrefix(params, "<") {
		return mouseAction(params[1:], final)
	}
	switch final {
	case "A":
		return KeyAction{Type: ActionUp}
	case "B":
		return KeyAction{Type: ActionDown}
	case "H":
		return KeyAction{Type: ActionHome}
	case "F":
		return KeyAction{Type: ActionEnd}
	case "~":
		switch params {
		case "5", "5;5":
			return KeyAction{Type: ActionPageUp}
		case "6", "6;5":
			return KeyAction{Type: ActionPageDown}
		case "1", "7":
			return KeyAction{Type: ActionHome}
		case "4", "8":
			return KeyAction{Type: ActionEnd}
		default:
			return KeyAction{Type: ActionIgnore}
		}
	default:
		return KeyAction{Type: ActionIgnore}
	}
}

func mouseAction(params string, final string) KeyAction {
	if final != "M" {
		// releases and motion don't trigger click action
		return KeyAction{Type: ActionIgnore}
	}
	parts := strings.Split(params, ";")
	if len(parts) < 3 {
		return KeyAction{Type: ActionIgnore}
	}
	btn, err1 := strconv.Atoi(parts[0])
	x, err2 := strconv.Atoi(parts[1])
	y, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return KeyAction{Type: ActionIgnore}
	}

	if btn == 0 {
		return KeyAction{Type: ActionClick, X: x - 1, Y: y - 1}
	}
	if btn == 64 {
		return KeyAction{Type: ActionWheelUp}
	}
	if btn == 65 {
		return KeyAction{Type: ActionWheelDown}
	}
	return KeyAction{Type: ActionIgnore}
}

// IsPrintable returns true if key is a normal printable ASCII or Unicode character.
func IsPrintable(key string) bool {
	if len(key) == 0 {
		return false
	}
	r := []rune(key)[0]
	return unicode.IsPrint(r)
}
