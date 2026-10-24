-- 移除过宽的 ORM 关键词规则，并立即清理由其造成的自动误分类。
-- topic='orm' 仍按精确 topic 匹配保留；人工归类不受影响。
DELETE FROM category_rules
WHERE category_id = (SELECT id FROM categories WHERE slug = 'data-database')
  AND match_type = 'keyword'
  AND pattern = 'orm';

DELETE FROM repo_categories
WHERE source = 'auto'
  AND category_id = (SELECT id FROM categories WHERE slug = 'data-database')
  AND NOT EXISTS (
      SELECT 1
      FROM repositories AS r
      JOIN category_rules AS rule
        ON rule.category_id = repo_categories.category_id
       AND rule.enabled = 1
       AND trim(rule.pattern) <> ''
      WHERE r.id = repo_categories.repo_id
        AND (
            (rule.match_type = 'topic' AND EXISTS (
                SELECT 1
                FROM json_each(CASE WHEN json_valid(r.topics_json) THEN r.topics_json ELSE '[]' END) AS topic
                WHERE lower(CAST(topic.value AS TEXT)) = lower(rule.pattern)
            ))
            OR (rule.match_type = 'keyword' AND instr(
                lower(r.name || char(10) || r.full_name || char(10) || COALESCE(r.description, '')),
                lower(rule.pattern)
            ) > 0)
            OR (rule.match_type = 'language' AND r.language IS NOT NULL
                AND r.language <> '' AND lower(r.language) = lower(rule.pattern))
        )
  );

-- 人工归类优先：旧版本可能在 manual 旁残留 auto 行，迁移时立即清理。
DELETE FROM repo_categories
WHERE source = 'auto'
  AND EXISTS (
      SELECT 1
      FROM repo_categories AS manual_assignment
      WHERE manual_assignment.repo_id = repo_categories.repo_id
        AND manual_assignment.source = 'manual'
  );

-- 删除误分类后，规范化纯自动归类：每个仓库恰好一个主分类。
-- 优先保留原主分类；没有主分类或存在多个主分类时按分类顺序稳定选一个。
UPDATE repo_categories
SET is_primary = CASE WHEN category_id = (
    SELECT automatic_assignment.category_id
    FROM repo_categories AS automatic_assignment
    JOIN categories AS category ON category.id = automatic_assignment.category_id
    WHERE automatic_assignment.repo_id = repo_categories.repo_id
      AND automatic_assignment.source = 'auto'
    ORDER BY automatic_assignment.is_primary DESC, category.sort_order, category.id
    LIMIT 1
) THEN 1 ELSE 0 END
WHERE source = 'auto';
