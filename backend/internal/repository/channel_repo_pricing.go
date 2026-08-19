package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// --- 模型定价 ---

func (r *channelRepository) ListModelPricing(ctx context.Context, channelID int64) ([]service.ChannelModelPricing, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, channel_id, platform, models, billing_mode, input_price, output_price, cache_write_price, cache_read_price, image_input_price, image_output_price, per_request_price, time_pricing, created_at, updated_at
		 FROM channel_model_pricing WHERE channel_id = $1 ORDER BY id`, channelID,
	)
	if err != nil {
		return nil, fmt.Errorf("list model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result, pricingIDs, err := scanModelPricingRows(rows)
	if err != nil {
		return nil, err
	}

	if len(pricingIDs) > 0 {
		intervalMap, err := r.batchLoadIntervals(ctx, pricingIDs)
		if err != nil {
			return nil, err
		}
		for i := range result {
			result[i].Intervals = intervalMap[result[i].ID]
		}
		versionMap, err := r.batchLoadTimeVersions(ctx, pricingIDs)
		if err != nil {
			return nil, err
		}
		for i := range result {
			result[i].TimeVersions = versionMap[result[i].ID]
		}
	}

	return result, nil
}

func (r *channelRepository) CreateModelPricing(ctx context.Context, pricing *service.ChannelModelPricing) error {
	return createModelPricingExec(ctx, r.db, pricing)
}

func (r *channelRepository) UpdateModelPricing(ctx context.Context, pricing *service.ChannelModelPricing) error {
	modelsJSON, err := json.Marshal(pricing.Models)
	if err != nil {
		return fmt.Errorf("marshal models: %w", err)
	}
	timePricingJSON, err := marshalChannelTimePricing(pricing.TimePricing)
	if err != nil {
		return err
	}
	billingMode := pricing.BillingMode
	if billingMode == "" {
		billingMode = service.BillingModeToken
	}
	updatePricing := func(exec dbExec) error {
		result, err := exec.ExecContext(ctx,
			`UPDATE channel_model_pricing
			 SET models = $1, billing_mode = $2, input_price = $3, output_price = $4, cache_write_price = $5, cache_read_price = $6, image_input_price = $7, image_output_price = $8, per_request_price = $9, time_pricing = $10, platform = $11, updated_at = NOW()
			 WHERE id = $12`,
			modelsJSON, billingMode, pricing.InputPrice, pricing.OutputPrice, pricing.CacheWritePrice, pricing.CacheReadPrice,
			pricing.ImageInputPrice, pricing.ImageOutputPrice, pricing.PerRequestPrice, timePricingJSON, pricing.Platform, pricing.ID,
		)
		if err != nil {
			return fmt.Errorf("update model pricing: %w", err)
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			return fmt.Errorf("pricing entry not found: %d", pricing.ID)
		}
		return nil
	}

	// A nil slice means an older caller did not send the versioned schedule.
	// Preserve existing versions unless a recurring schedule is being enabled;
	// the two schedule systems must never coexist and compound billing multipliers.
	if pricing.TimeVersions == nil {
		if pricing.TimePricing != nil && len(pricing.TimePricing.Periods) > 0 {
			return r.runInTx(ctx, func(tx *sql.Tx) error {
				if err := updatePricing(tx); err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM channel_model_pricing_versions WHERE pricing_id = $1`, pricing.ID); err != nil {
					return fmt.Errorf("delete old time pricing versions: %w", err)
				}
				return nil
			})
		}
		return updatePricing(r.db)
	}
	return r.runInTx(ctx, func(tx *sql.Tx) error {
		if err := updatePricing(tx); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM channel_model_pricing_versions WHERE pricing_id = $1`, pricing.ID); err != nil {
			return fmt.Errorf("delete old time pricing versions: %w", err)
		}
		for i := range pricing.TimeVersions {
			pricing.TimeVersions[i].PricingID = pricing.ID
			if err := createTimeVersionExec(ctx, tx, &pricing.TimeVersions[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *channelRepository) DeleteModelPricing(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM channel_model_pricing WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete model pricing: %w", err)
	}
	return nil
}

func (r *channelRepository) ReplaceModelPricing(ctx context.Context, channelID int64, pricingList []service.ChannelModelPricing) error {
	return r.runInTx(ctx, func(tx *sql.Tx) error {
		return replaceModelPricingTx(ctx, tx, channelID, pricingList)
	})
}

// --- 批量加载辅助方法 ---

// batchLoadModelPricing 批量加载多个渠道的模型定价（含区间）
func (r *channelRepository) batchLoadModelPricing(ctx context.Context, channelIDs []int64) (map[int64][]service.ChannelModelPricing, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, channel_id, platform, models, billing_mode, input_price, output_price, cache_write_price, cache_read_price, image_input_price, image_output_price, per_request_price, time_pricing, created_at, updated_at
		 FROM channel_model_pricing WHERE channel_id = ANY($1) ORDER BY channel_id, id`,
		pq.Array(channelIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load model pricing: %w", err)
	}
	defer func() { _ = rows.Close() }()

	allPricing, allPricingIDs, err := scanModelPricingRows(rows)
	if err != nil {
		return nil, err
	}

	// 按 channelID 分组
	pricingMap := make(map[int64][]service.ChannelModelPricing, len(channelIDs))
	for _, p := range allPricing {
		pricingMap[p.ChannelID] = append(pricingMap[p.ChannelID], p)
	}

	// 批量加载所有区间
	if len(allPricingIDs) > 0 {
		intervalMap, err := r.batchLoadIntervals(ctx, allPricingIDs)
		if err != nil {
			return nil, err
		}
		for chID := range pricingMap {
			for i := range pricingMap[chID] {
				pricingMap[chID][i].Intervals = intervalMap[pricingMap[chID][i].ID]
			}
		}
		versionMap, err := r.batchLoadTimeVersions(ctx, allPricingIDs)
		if err != nil {
			return nil, err
		}
		for chID := range pricingMap {
			for i := range pricingMap[chID] {
				pricingMap[chID][i].TimeVersions = versionMap[pricingMap[chID][i].ID]
			}
		}
	}

	return pricingMap, nil
}

// batchLoadIntervals 批量加载多个定价条目的区间
func (r *channelRepository) batchLoadIntervals(ctx context.Context, pricingIDs []int64) (map[int64][]service.PricingInterval, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, pricing_id, min_tokens, max_tokens, tier_label,
		        input_price, output_price, cache_write_price, cache_read_price,
		        per_request_price, sort_order, created_at, updated_at
		 FROM channel_pricing_intervals
		 WHERE pricing_id = ANY($1) ORDER BY pricing_id, sort_order, id`,
		pq.Array(pricingIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load intervals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	intervalMap := make(map[int64][]service.PricingInterval, len(pricingIDs))
	for rows.Next() {
		var iv service.PricingInterval
		if err := rows.Scan(
			&iv.ID, &iv.PricingID, &iv.MinTokens, &iv.MaxTokens, &iv.TierLabel,
			&iv.InputPrice, &iv.OutputPrice, &iv.CacheWritePrice, &iv.CacheReadPrice,
			&iv.PerRequestPrice, &iv.SortOrder, &iv.CreatedAt, &iv.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan interval: %w", err)
		}
		intervalMap[iv.PricingID] = append(intervalMap[iv.PricingID], iv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate intervals: %w", err)
	}
	return intervalMap, nil
}

func (r *channelRepository) batchLoadTimeVersions(ctx context.Context, pricingIDs []int64) (map[int64][]service.PricingTimeVersion, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, pricing_id, effective_from, effective_until, timezone, default_multiplier,
		        input_price, output_price, cache_write_price, cache_read_price,
		        image_input_price, image_output_price, sort_order, created_at, updated_at
		 FROM channel_model_pricing_versions
		 WHERE pricing_id = ANY($1) ORDER BY pricing_id, effective_from, sort_order, id`,
		pq.Array(pricingIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load time pricing versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	versionMap := make(map[int64][]service.PricingTimeVersion, len(pricingIDs))
	versionIDs := make([]int64, 0)
	for rows.Next() {
		var version service.PricingTimeVersion
		if err := rows.Scan(
			&version.ID, &version.PricingID, &version.EffectiveFrom, &version.EffectiveUntil,
			&version.Timezone, &version.DefaultMultiplier, &version.InputPrice, &version.OutputPrice,
			&version.CacheWritePrice, &version.CacheReadPrice, &version.ImageInputPrice,
			&version.ImageOutputPrice, &version.SortOrder, &version.CreatedAt, &version.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan time pricing version: %w", err)
		}
		versionIDs = append(versionIDs, version.ID)
		versionMap[version.PricingID] = append(versionMap[version.PricingID], version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate time pricing versions: %w", err)
	}
	if len(versionIDs) == 0 {
		return versionMap, nil
	}

	windowRows, err := r.db.QueryContext(ctx,
		`SELECT id, version_id, label, weekdays, start_minute, end_minute,
		        multiplier, sort_order, created_at, updated_at
		 FROM channel_pricing_time_windows
		 WHERE version_id = ANY($1) ORDER BY version_id, sort_order, id`,
		pq.Array(versionIDs),
	)
	if err != nil {
		return nil, fmt.Errorf("batch load pricing time windows: %w", err)
	}
	defer func() { _ = windowRows.Close() }()
	windowsByVersion := make(map[int64][]service.PricingTimeWindow, len(versionIDs))
	for windowRows.Next() {
		var window service.PricingTimeWindow
		if err := windowRows.Scan(
			&window.ID, &window.VersionID, &window.Label, &window.Weekdays,
			&window.StartMinute, &window.EndMinute, &window.Multiplier,
			&window.SortOrder, &window.CreatedAt, &window.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan pricing time window: %w", err)
		}
		windowsByVersion[window.VersionID] = append(windowsByVersion[window.VersionID], window)
	}
	if err := windowRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pricing time windows: %w", err)
	}
	for pricingID := range versionMap {
		for i := range versionMap[pricingID] {
			versionMap[pricingID][i].Windows = windowsByVersion[versionMap[pricingID][i].ID]
		}
	}
	return versionMap, nil
}

// --- 共享 scan 辅助 ---

// scanModelPricingRows 扫描 model pricing 行，返回结果列表和 ID 列表
func scanModelPricingRows(rows *sql.Rows) ([]service.ChannelModelPricing, []int64, error) {
	var result []service.ChannelModelPricing
	var pricingIDs []int64
	for rows.Next() {
		var p service.ChannelModelPricing
		var modelsJSON []byte
		var timePricingJSON []byte
		if err := rows.Scan(
			&p.ID, &p.ChannelID, &p.Platform, &modelsJSON, &p.BillingMode,
			&p.InputPrice, &p.OutputPrice, &p.CacheWritePrice, &p.CacheReadPrice,
			&p.ImageInputPrice, &p.ImageOutputPrice, &p.PerRequestPrice, &timePricingJSON, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, nil, fmt.Errorf("scan model pricing: %w", err)
		}
		if err := json.Unmarshal(modelsJSON, &p.Models); err != nil {
			p.Models = []string{}
		}
		timePricing, err := unmarshalChannelTimePricing(timePricingJSON)
		if err != nil {
			return nil, nil, err
		}
		p.TimePricing = timePricing
		pricingIDs = append(pricingIDs, p.ID)
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate model pricing: %w", err)
	}
	return result, pricingIDs, nil
}

// --- 事务内辅助方法 ---

// dbExec 是 *sql.DB 和 *sql.Tx 共享的最小 SQL 执行接口
type dbExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func setGroupIDsTx(ctx context.Context, exec dbExec, channelID int64, groupIDs []int64) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM channel_groups WHERE channel_id = $1`, channelID); err != nil {
		return fmt.Errorf("delete old group associations: %w", err)
	}
	if len(groupIDs) == 0 {
		return nil
	}
	_, err := exec.ExecContext(ctx,
		`INSERT INTO channel_groups (channel_id, group_id)
		 SELECT $1, unnest($2::bigint[])`,
		channelID, pq.Array(groupIDs),
	)
	if err != nil {
		return fmt.Errorf("insert group associations: %w", err)
	}
	return nil
}

func createModelPricingExec(ctx context.Context, exec dbExec, pricing *service.ChannelModelPricing) error {
	modelsJSON, err := json.Marshal(pricing.Models)
	if err != nil {
		return fmt.Errorf("marshal models: %w", err)
	}
	timePricingJSON, err := marshalChannelTimePricing(pricing.TimePricing)
	if err != nil {
		return err
	}
	billingMode := pricing.BillingMode
	if billingMode == "" {
		billingMode = service.BillingModeToken
	}
	platform := pricing.Platform
	if platform == "" {
		platform = "anthropic"
	}
	err = exec.QueryRowContext(ctx,
		`INSERT INTO channel_model_pricing (channel_id, platform, models, billing_mode, input_price, output_price, cache_write_price, cache_read_price, image_input_price, image_output_price, per_request_price, time_pricing)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING id, created_at, updated_at`,
		pricing.ChannelID, platform, modelsJSON, billingMode,
		pricing.InputPrice, pricing.OutputPrice, pricing.CacheWritePrice, pricing.CacheReadPrice,
		pricing.ImageInputPrice, pricing.ImageOutputPrice, pricing.PerRequestPrice, timePricingJSON,
	).Scan(&pricing.ID, &pricing.CreatedAt, &pricing.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert model pricing: %w", err)
	}

	for i := range pricing.Intervals {
		pricing.Intervals[i].PricingID = pricing.ID
		if err := createIntervalExec(ctx, exec, &pricing.Intervals[i]); err != nil {
			return err
		}
	}
	for i := range pricing.TimeVersions {
		pricing.TimeVersions[i].PricingID = pricing.ID
		if err := createTimeVersionExec(ctx, exec, &pricing.TimeVersions[i]); err != nil {
			return err
		}
	}

	return nil
}

func marshalChannelTimePricing(config *service.ChannelTimePricing) (any, error) {
	if config == nil || len(config.Periods) == 0 {
		return nil, nil
	}
	data, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal time pricing: %w", err)
	}
	return string(data), nil
}

func unmarshalChannelTimePricing(data []byte) (*service.ChannelTimePricing, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var config service.ChannelTimePricing
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("unmarshal time pricing: %w", err)
	}
	return &config, nil
}

func createIntervalExec(ctx context.Context, exec dbExec, iv *service.PricingInterval) error {
	return exec.QueryRowContext(ctx,
		`INSERT INTO channel_pricing_intervals
		 (pricing_id, min_tokens, max_tokens, tier_label, input_price, output_price, cache_write_price, cache_read_price, per_request_price, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id, created_at, updated_at`,
		iv.PricingID, iv.MinTokens, iv.MaxTokens, iv.TierLabel,
		iv.InputPrice, iv.OutputPrice, iv.CacheWritePrice, iv.CacheReadPrice,
		iv.PerRequestPrice, iv.SortOrder,
	).Scan(&iv.ID, &iv.CreatedAt, &iv.UpdatedAt)
}

func createTimeVersionExec(ctx context.Context, exec dbExec, version *service.PricingTimeVersion) error {
	if strings.TrimSpace(version.Timezone) == "" {
		version.Timezone = "Asia/Shanghai"
	}
	err := exec.QueryRowContext(ctx,
		`INSERT INTO channel_model_pricing_versions
		 (pricing_id, effective_from, effective_until, timezone, default_multiplier,
		  input_price, output_price, cache_write_price, cache_read_price,
		  image_input_price, image_output_price, sort_order)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING id, created_at, updated_at`,
		version.PricingID, version.EffectiveFrom, version.EffectiveUntil, version.Timezone,
		version.DefaultMultiplier, version.InputPrice, version.OutputPrice,
		version.CacheWritePrice, version.CacheReadPrice, version.ImageInputPrice,
		version.ImageOutputPrice, version.SortOrder,
	).Scan(&version.ID, &version.CreatedAt, &version.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert time pricing version: %w", err)
	}
	for i := range version.Windows {
		window := &version.Windows[i]
		window.VersionID = version.ID
		if err := exec.QueryRowContext(ctx,
			`INSERT INTO channel_pricing_time_windows
			 (version_id, label, weekdays, start_minute, end_minute, multiplier, sort_order)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)
			 RETURNING id, created_at, updated_at`,
			window.VersionID, window.Label, window.Weekdays, window.StartMinute,
			window.EndMinute, window.Multiplier, window.SortOrder,
		).Scan(&window.ID, &window.CreatedAt, &window.UpdatedAt); err != nil {
			return fmt.Errorf("insert pricing time window: %w", err)
		}
	}
	return nil
}

func replaceModelPricingTx(ctx context.Context, exec dbExec, channelID int64, pricingList []service.ChannelModelPricing) error {
	if _, err := exec.ExecContext(ctx, `DELETE FROM channel_model_pricing WHERE channel_id = $1`, channelID); err != nil {
		return fmt.Errorf("delete old model pricing: %w", err)
	}
	for i := range pricingList {
		pricingList[i].ChannelID = channelID
		if err := createModelPricingExec(ctx, exec, &pricingList[i]); err != nil {
			return fmt.Errorf("insert model pricing: %w", err)
		}
	}
	return nil
}

// isUniqueViolation 检查 pq 唯一约束违反错误
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr != nil {
		return pqErr.Code == "23505"
	}
	return false
}

// escapeLike 转义 LIKE/ILIKE 模式中的特殊字符
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
