package nodes

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Found 是在常用目录里找到的一个可导入文件。
type Found struct {
	Path     string
	Nodes    int
	Servers  int
	Modified time.Time
	// Subscription 为真表示文件带自动更新地址（发布工具生成的订阅文件）。
	Subscription bool
}

const maxFindSize = 512 << 10

// Find 扫描 dirs（不递归），返回能解析出真实节点的 .json / .txt 文件。
// 订阅文件排在前面，其余按修改时间从新到旧；内容相同的文件只保留一个。
func Find(dirs []string) []Found {
	var out []Found
	seenPath := map[string]bool{}
	seenText := map[string]bool{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if e.IsDir() || (ext != ".json" && ext != ".txt") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			key := strings.ToLower(filepath.Clean(p))
			if seenPath[key] {
				continue
			}
			seenPath[key] = true
			info, err := e.Info()
			if err != nil || info.Size() == 0 || info.Size() > maxFindSize {
				continue
			}
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			text := strings.TrimSpace(string(b))
			if seenText[text] {
				continue
			}
			list, err := Parse(text)
			if err != nil || len(list) == 0 || AllExamples(list) {
				continue
			}
			seenText[text] = true
			servers := map[string]bool{}
			for _, n := range list {
				servers[n.Server] = true
			}
			out = append(out, Found{
				Path: p, Nodes: len(list), Servers: len(servers), Modified: info.ModTime(),
				Subscription: strings.Contains(text, `"update_key"`),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Subscription != out[j].Subscription {
			return out[i].Subscription
		}
		return out[i].Modified.After(out[j].Modified)
	})
	return out
}
