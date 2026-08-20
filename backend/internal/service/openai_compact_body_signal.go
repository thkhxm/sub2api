package service

import "github.com/tidwall/gjson"

// HasCompactionTriggerInInput 检测 input 中 type="compaction_trigger" 的条目。
// handler 会结合 path、stream 和 Codex beta feature 请求头，区分原生 remote
// compaction v2 与需要 /responses/compact 兼容桥的旧 body-signal 请求。
func HasCompactionTriggerInInput(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return false
	}
	found := false
	input.ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "compaction_trigger" {
			found = true
			return false
		}
		return true
	})
	return found
}
