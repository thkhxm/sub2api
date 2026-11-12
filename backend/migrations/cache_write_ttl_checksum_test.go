package migrations

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

func TestCacheWriteTTLAppliedMigrationChecksum(t *testing.T) {
	content, err := FS.ReadFile("250_openai_cache_write_ttl.sql")
	if err != nil {
		t.Fatal(err)
	}
	// 与生产已登记的原始字节一致；修补只能新增迁移，不能重写已有校验和。
	const applied = "af27b30046c831736575f4754960a86b1be17c543c318e98605684ba73161270"
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(string(content))))); got != applied {
		t.Fatalf("已应用迁移字节被改变：got %s, want %s；检查 Git 换行转换", got, applied)
	}
}
