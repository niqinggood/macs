package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"mac/internal/configvalue"
)

const maxRobotQuickCommandRunes = 100

var (
	robotQuickNumberPattern = `([0-9]+(?:\.[0-9]+)?|[零〇一二两三四五六七八九十百点半]+)`

	robotChineseStopPattern = regexp.MustCompile(`^(?:请)?(?:(?:让)?(?:小车|机器人))?(?:立即|马上)?(?:停|停止|停下|急停|刹车)(?:一下)?[。.!！]?$`)
	robotEnglishStopPattern = regexp.MustCompile(`^(?:please\s+)?(?:(?:robot|rover)\s+)?(?:stop|halt|emergency\s+stop)(?:\s+now)?[.!]?$`)
	robotChineseStopIntent  = regexp.MustCompile(`(?:急停|停止|停下|刹车|不要动|别动|停)`)
	robotEnglishStopIntent  = regexp.MustCompile(`\b(?:stop|halt)\b`)

	robotChineseTranslatePattern = regexp.MustCompile(`^(?:请)?(?:(?:让)?(?:小车|机器人))?(前进|向前|往前|后退|向后|往后)\s*` + robotQuickNumberPattern + `\s*(厘米|公分|cm|米|m)[。.!！]?$`)
	robotEnglishTranslatePattern = regexp.MustCompile(`^(?:please\s+)?(?:(?:robot|rover)\s+)?(?:move|go)\s+(forward|backward|backwards)\s+` + robotQuickNumberPattern + `\s*(centimeters?|centimetres?|cm|meters?|metres?|m)[.!]?$`)

	robotChineseRotatePattern = regexp.MustCompile(`^(?:请)?(?:(?:让)?(?:小车|机器人))?(左转|向左转|往左转|右转|向右转|往右转)\s*` + robotQuickNumberPattern + `\s*(度|°)[。.!！]?$`)
	robotEnglishRotatePattern = regexp.MustCompile(`^(?:please\s+)?(?:(?:robot|rover)\s+)?(?:turn|rotate)\s+(left|right)\s+` + robotQuickNumberPattern + `\s*(degrees?|deg|°)[.!]?$`)
)

type robotQuickIntent struct {
	Action    string  `json:"action"`
	Motion    string  `json:"motion,omitempty"`
	DistanceM float64 `json:"distance_m,omitempty"`
	AngleDeg  float64 `json:"angle_deg,omitempty"`
	Language  string  `json:"language"`
}

type RobotQuickIntent = robotQuickIntent

func normalizeRobotQuickText(text string) string {
	replacer := strings.NewReplacer(
		"０", "0", "１", "1", "２", "2", "３", "3", "４", "4",
		"５", "5", "６", "6", "７", "7", "８", "8", "９", "9",
		"，", ",", "．", ".", "　", " ",
	)
	return strings.ToLower(strings.TrimSpace(replacer.Replace(text)))
}

func parseRobotQuickCommand(text string) (robotQuickIntent, error) {
	text = normalizeRobotQuickText(text)
	if text == "" {
		return robotQuickIntent{}, fmt.Errorf("robot command text is required")
	}
	if utf8.RuneCountInString(text) > maxRobotQuickCommandRunes {
		return robotQuickIntent{}, fmt.Errorf("robot command text must be at most %d characters", maxRobotQuickCommandRunes)
	}

	if robotChineseStopPattern.MatchString(text) || robotChineseStopIntent.MatchString(text) {
		return robotQuickIntent{Action: "stop", Language: "zh"}, nil
	}
	if robotEnglishStopPattern.MatchString(text) || robotEnglishStopIntent.MatchString(text) {
		return robotQuickIntent{Action: "stop", Language: "en"}, nil
	}
	if matches := robotChineseTranslatePattern.FindStringSubmatch(text); matches != nil {
		value, err := parseRobotQuickNumber(matches[2])
		if err != nil {
			return robotQuickIntent{}, err
		}
		distance := robotDistanceToMetres(value, matches[3])
		if matches[1] == "后退" || matches[1] == "向后" || matches[1] == "往后" {
			distance = -distance
		}
		return robotQuickIntent{Action: "move", Motion: "translate", DistanceM: distance, Language: "zh"}, nil
	}
	if matches := robotEnglishTranslatePattern.FindStringSubmatch(text); matches != nil {
		value, err := parseRobotQuickNumber(matches[2])
		if err != nil {
			return robotQuickIntent{}, err
		}
		distance := robotDistanceToMetres(value, matches[3])
		if matches[1] == "backward" || matches[1] == "backwards" {
			distance = -distance
		}
		return robotQuickIntent{Action: "move", Motion: "translate", DistanceM: distance, Language: "en"}, nil
	}
	if matches := robotChineseRotatePattern.FindStringSubmatch(text); matches != nil {
		value, err := parseRobotQuickNumber(matches[2])
		if err != nil {
			return robotQuickIntent{}, err
		}
		if matches[1] == "右转" || matches[1] == "向右转" || matches[1] == "往右转" {
			value = -value
		}
		return robotQuickIntent{Action: "move", Motion: "rotate", AngleDeg: value, Language: "zh"}, nil
	}
	if matches := robotEnglishRotatePattern.FindStringSubmatch(text); matches != nil {
		value, err := parseRobotQuickNumber(matches[2])
		if err != nil {
			return robotQuickIntent{}, err
		}
		if matches[1] == "right" {
			value = -value
		}
		return robotQuickIntent{Action: "move", Motion: "rotate", AngleDeg: value, Language: "en"}, nil
	}
	return robotQuickIntent{}, fmt.Errorf("unsupported or ambiguous robot command; use exactly one of: stop, forward/backward with cm or m, or left/right with degrees")
}

func ParseRobotQuickCommand(text string) (RobotQuickIntent, error) {
	return parseRobotQuickCommand(text)
}

func robotDistanceToMetres(value float64, unit string) float64 {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "厘米", "公分", "cm", "centimeter", "centimeters", "centimetre", "centimetres":
		return value / 100
	default:
		return value
	}
}

func parseRobotQuickNumber(raw string) (float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("robot command number is required")
	}
	if value, err := strconv.ParseFloat(raw, 64); err == nil {
		if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
			return 0, fmt.Errorf("robot command number must be finite and greater than zero")
		}
		return value, nil
	}
	if raw == "半" {
		return 0.5, nil
	}

	parts := strings.Split(raw, "点")
	if len(parts) > 2 {
		return 0, fmt.Errorf("invalid Chinese robot command number %q", raw)
	}
	integer, err := parseRobotChineseInteger(parts[0])
	if err != nil {
		return 0, err
	}
	value := float64(integer)
	if len(parts) == 2 {
		if parts[1] == "" {
			return 0, fmt.Errorf("invalid Chinese robot command number %q", raw)
		}
		scale := 0.1
		for _, digitRune := range parts[1] {
			digit, ok := robotChineseDigit(digitRune)
			if !ok {
				return 0, fmt.Errorf("invalid Chinese decimal digit in %q", raw)
			}
			value += float64(digit) * scale
			scale /= 10
		}
	}
	if value <= 0 {
		return 0, fmt.Errorf("robot command number must be greater than zero")
	}
	return value, nil
}

func parseRobotChineseInteger(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	total := 0
	current := 0
	seen := false
	for _, char := range raw {
		if digit, ok := robotChineseDigit(char); ok {
			current = digit
			seen = true
			continue
		}
		unit := 0
		switch char {
		case '十':
			unit = 10
		case '百':
			unit = 100
		default:
			return 0, fmt.Errorf("invalid Chinese robot command number %q", raw)
		}
		if current == 0 {
			current = 1
		}
		total += current * unit
		current = 0
		seen = true
	}
	if !seen {
		return 0, fmt.Errorf("invalid Chinese robot command number %q", raw)
	}
	return total + current, nil
}

func robotChineseDigit(char rune) (int, bool) {
	switch char {
	case '零', '〇':
		return 0, true
	case '一':
		return 1, true
	case '二', '两':
		return 2, true
	case '三':
		return 3, true
	case '四':
		return 4, true
	case '五':
		return 5, true
	case '六':
		return 6, true
	case '七':
		return 7, true
	case '八':
		return 8, true
	case '九':
		return 9, true
	default:
		return 0, false
	}
}

type RobotQuickCommandTool struct{ hub *RobotRuntimeHub }

func NewRobotQuickCommandTool(hub *RobotRuntimeHub) *RobotQuickCommandTool {
	return &RobotQuickCommandTool{hub: hub}
}

func (t *RobotQuickCommandTool) Name() string { return "robot_command" }
func (t *RobotQuickCommandTool) Description() string {
	return "Parse and execute one strict short Chinese or English robot command: stop; forward/backward with an explicit cm or m distance; or left/right with an explicit degree angle. " +
		"Use this for short natural-language or ASR output. It rejects missing units, multiple steps, navigation goals, arm commands, and ambiguous prose, then routes accepted motion through the same limits, watchdog, idempotency, and audit runtime as robot_move."
}
func (t *RobotQuickCommandTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"text": map[string]interface{}{
				"type":        "string",
				"maxLength":   maxRobotQuickCommandRunes,
				"description": "One short command, for example 前进20厘米, 右转90度, stop, or move backward 0.2 m.",
			},
			"device": map[string]interface{}{
				"type":        "string",
				"description": "Configured robot device name; defaults to tools_config.robot.default_device.",
			},
			"command_id": map[string]interface{}{
				"type":        "string",
				"maxLength":   maxRobotCommandIDLength,
				"description": "Stable ID for idempotent motion retry. The host generates one when omitted.",
			},
		},
		"required": []string{"text"},
	}
}
func (t *RobotQuickCommandTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	result, err := executeRobotQuickCommand(
		ctx,
		t.hub,
		configvalue.String(params, "text", ""),
		configvalue.String(params, "device", ""),
		configvalue.String(params, "command_id", ""),
	)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func executeRobotQuickCommand(ctx context.Context, hub *RobotRuntimeHub, text, device, commandID string) (map[string]interface{}, error) {
	if hub == nil {
		return nil, fmt.Errorf("robot runtime is unavailable")
	}
	intent, err := parseRobotQuickCommand(text)
	if err != nil {
		return nil, err
	}
	result := map[string]interface{}{"ok": true, "parsed_intent": intent}
	if intent.Action == "stop" {
		receipt, stopErr := hub.Stop(ctx, map[string]interface{}{
			"device": device, "command_id": commandID, "reason": "strict natural-language stop command",
		})
		if stopErr != nil {
			return nil, stopErr
		}
		result["result"] = receipt
	} else {
		moveParams := map[string]interface{}{
			"device": device, "command_id": commandID, "motion": intent.Motion,
		}
		if intent.Motion == "translate" {
			moveParams["distance_m"] = intent.DistanceM
		} else {
			moveParams["angle_deg"] = intent.AngleDeg
		}
		receipt, moveErr := hub.Move(ctx, moveParams)
		if moveErr != nil {
			return nil, moveErr
		}
		result["result"] = receipt
	}
	return result, nil
}

func ExecuteRobotQuickCommand(ctx context.Context, hub *RobotRuntimeHub, text, device, commandID string) (map[string]interface{}, error) {
	return executeRobotQuickCommand(ctx, hub, text, device, commandID)
}
func (t *RobotQuickCommandTool) IsReadOnly() bool      { return false }
func (t *RobotQuickCommandTool) IsNeverParallel() bool { return true }
func (t *RobotQuickCommandTool) MaxResultSize() int    { return 7000 }
