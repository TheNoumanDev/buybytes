package sweep

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Cleaned summarises one cleanup, for the share post. It holds only sizes and
// categories, never paths or project names, so the post leaks nothing private.
type Cleaned struct {
	Bytes      int64              `json:"bytes"`
	Items      int                `json:"items"`
	ByCategory map[Category]int64 `json:"by_category"`
	Counts     map[Category]int   `json:"counts"`
	When       time.Time          `json:"when"`
}

// Summarize builds a Cleaned from the items that were removed.
func Summarize(removed []Item) Cleaned {
	c := Cleaned{ByCategory: map[Category]int64{}, Counts: map[Category]int{}, When: time.Now()}
	for _, it := range removed {
		c.Bytes += it.Size
		c.Items++
		c.ByCategory[it.Category] += it.Size
		c.Counts[it.Category]++
	}
	return c
}

// roasts are picked from what took the most space. {size} and {n} are filled in.
var roasts = map[Category][]string{
	CatAgents: {
		"my coding agents left {n} worktrees behind. even they ghost their own work.",
		"{size} of agent worktrees. i'm not a founder, i'm a landlord for bots.",
	},
	CatDev: {
		"{size} of node_modules for side projects i quit in week 2. growth mindset.",
		"{size} of build folders. for apps with zero users. respectfully.",
	},
	CatToolchain: {
		"had {n} SDK versions installed. shipped with one. maybe.",
		"kept every SDK version like they were gonna text back.",
	},
	CatDevices: {
		"{size} of simulators for phones i was never gonna test on. delusional.",
		"my emulators had more storage than my actual phone. and more screen time.",
	},
	CatAI: {
		"{size} of AI models i downloaded once to feel smart.",
		"hoarded AI models like pokémon cards. caught them all, used none.",
	},
	CatAIData: {
		"deleted my old AI chats. it won't remember me either. we're even.",
		"{size} of me asking AI to fix bugs i wrote.",
	},
	CatDownloads: {
		"found a whole season in my downloads. watched one episode.",
		"{n} things in downloads i opened once. like my gym membership.",
	},
	CatCaches: {
		"my mac was holding onto cache harder than i hold onto grudges.",
		"{size} of cache. my mac said \"just in case\" more than my mom.",
	},
}

// SharePost writes a short post: how much got freed, then a roast based on
// what took the most space. It fits in one tweet.
func SharePost(c Cleaned, seed int64) string {
	r := rand.New(rand.NewSource(seed))
	var top Category
	for cat, b := range c.ByCategory {
		if top == "" || b > c.ByCategory[top] {
			top = cat
		}
	}
	roast := "turns out my disk wasn't full. my habits were."
	if lines := roasts[top]; len(lines) > 0 {
		roast = lines[r.Intn(len(lines))]
		roast = strings.ReplaceAll(roast, "{size}", HumanSize(c.ByCategory[top]))
		roast = strings.ReplaceAll(roast, "{n}", fmt.Sprint(c.Counts[top]))
	}
	if c.Bytes >= 50<<30 && r.Intn(2) == 0 {
		roast = "that wasn't a mac. that was a landfill with a keyboard."
	}
	return fmt.Sprintf("freed %s on my mac 🧹\n\n%s\n\nnpx buybytes", HumanSize(c.Bytes), roast)
}

// PostURL opens X with the post filled in. The user still presses Post.
func PostURL(post string) string {
	return "https://x.com/intent/post?text=" + url.QueryEscape(post)
}

func lastPath(home string) string {
	return filepath.Join(home, ".config", "buybytes", "last.json")
}

// SaveLast remembers the latest cleanup for `buybytes share`.
func SaveLast(home string, c Cleaned) error {
	if err := os.MkdirAll(filepath.Dir(lastPath(home)), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(lastPath(home), b, 0o644)
}

// LoadLast reads the latest cleanup.
func LoadLast(home string) (Cleaned, error) {
	var c Cleaned
	b, err := os.ReadFile(lastPath(home))
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}
