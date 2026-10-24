// Package classify 按数据库配置的分类规则（spec 4.4）为仓库归类。
// 规则全部来自 category_rules/categories 表，不在代码中硬编码；
// 无命中的仓库不写任何归类行，仍保留在「全部项目」。
package classify

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// MatchType 是 category_rules.match_type 的取值。
type MatchType string

const (
	MatchTopic    MatchType = "topic"
	MatchKeyword  MatchType = "keyword"
	MatchLanguage MatchType = "language"
)

// Rule 是一条启用中的分类匹配规则。
type Rule struct {
	ID         int64
	CategoryID int64
	MatchType  MatchType
	Pattern    string
	Priority   int
}

type category struct {
	id        int64
	slug      string
	sortOrder int
}

// Engine 持有启用规则与分类元数据，可并发复用（加载后只读）。
type Engine struct {
	rules      []Rule
	categories map[int64]category
}

// LoadEngine 读取所有启用规则（仅关联启用分类）构建分类引擎。
func LoadEngine(ctx context.Context, database *sql.DB) (*Engine, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT c.id, c.slug, c.sort_order, r.id, r.match_type, r.pattern, r.priority
		FROM category_rules AS r
		JOIN categories AS c ON c.id = r.category_id
		WHERE r.enabled = 1 AND c.enabled = 1 AND trim(r.pattern) <> ''
		ORDER BY c.sort_order, c.id, r.priority DESC, r.id`)
	if err != nil {
		return nil, fmt.Errorf("load category rules: %w", err)
	}
	defer rows.Close()

	engine := &Engine{categories: make(map[int64]category)}
	for rows.Next() {
		var categoryID, ruleID int64
		var slug, matchType, pattern string
		var sortOrder, priority int
		if err := rows.Scan(&categoryID, &slug, &sortOrder, &ruleID, &matchType, &pattern, &priority); err != nil {
			return nil, fmt.Errorf("scan category rule: %w", err)
		}
		engine.rules = append(engine.rules, Rule{
			ID: ruleID, CategoryID: categoryID,
			MatchType: MatchType(matchType), Pattern: pattern, Priority: priority,
		})
		engine.categories[categoryID] = category{id: categoryID, slug: slug, sortOrder: sortOrder}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate category rules: %w", err)
	}
	return engine, nil
}

// Repo 是参与匹配的仓库字段。
type Repo struct {
	Name        string
	FullName    string
	Description string
	Language    string
	Topics      []string
}

// Match 是一次归类命中；IsPrimary 仅在代表主分类的元素上为 true。
type Match struct {
	CategoryID int64
	Slug       string
	Priority   int
	IsPrimary  bool
}

// ClassifyRepo 返回命中的全部分类：主分类排在首位（规则 priority 最高者，
// 同分取分类 sort_order 小者，再取分类 id 小者），其余按同序排列。无命中返回空。
func (engine *Engine) ClassifyRepo(repo Repo) []Match {
	if engine == nil || len(engine.rules) == 0 {
		return nil
	}
	haystack := strings.ToLower(
		strings.Join([]string{repo.Name, repo.FullName, repo.Description}, "\n"))
	best := make(map[int64]int)
	for _, rule := range engine.rules {
		if rule.Priority <= best[rule.CategoryID] {
			continue
		}
		if rule.matches(repo, haystack) {
			best[rule.CategoryID] = rule.Priority
		}
	}
	if len(best) == 0 {
		return nil
	}
	matches := make([]Match, 0, len(best))
	for categoryID, priority := range best {
		matches = append(matches, Match{
			CategoryID: categoryID,
			Slug:       engine.categories[categoryID].slug,
			Priority:   priority,
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Priority != matches[j].Priority {
			return matches[i].Priority > matches[j].Priority
		}
		left, right := engine.categories[matches[i].CategoryID], engine.categories[matches[j].CategoryID]
		if left.sortOrder != right.sortOrder {
			return left.sortOrder < right.sortOrder
		}
		return left.id < right.id
	})
	matches[0].IsPrimary = true
	return matches
}

// matches 判定单条规则是否命中：topic 精确匹配（大小写不敏感）、
// keyword 对 name+full_name+description 做不区分大小写子串匹配、
// language 大小写不敏感精确匹配。
func (rule Rule) matches(repo Repo, lowerHaystack string) bool {
	switch rule.MatchType {
	case MatchTopic:
		for _, topic := range repo.Topics {
			if strings.EqualFold(topic, rule.Pattern) {
				return true
			}
		}
		return false
	case MatchKeyword:
		return strings.Contains(lowerHaystack, strings.ToLower(rule.Pattern))
	case MatchLanguage:
		return repo.Language != "" && strings.EqualFold(repo.Language, rule.Pattern)
	default:
		return false
	}
}
