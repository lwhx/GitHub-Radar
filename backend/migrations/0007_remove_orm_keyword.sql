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
