package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type agisoOrderRepository struct {
	db *sql.DB
}

func NewAgisoOrderRepository(db *sql.DB) service.AgisoOrderRepository {
	return &agisoOrderRepository{db: db}
}

func (r *agisoOrderRepository) Ensure(ctx context.Context, event service.AgisoDeliveryEvent) (*service.AgisoOrder, error) {
	raw := sanitizeAgisoRawJSON(event.RawJSON)
	_, err := r.db.ExecContext(ctx, `
INSERT INTO agiso_orders (biz_order_id, item_id, raw_json, status)
VALUES ($1, $2, $3::jsonb, $4)
ON CONFLICT (biz_order_id) DO UPDATE
SET item_id = CASE WHEN agiso_orders.item_id = '' THEN EXCLUDED.item_id ELSE agiso_orders.item_id END,
    raw_json = CASE WHEN agiso_orders.raw_json = '{}'::jsonb THEN EXCLUDED.raw_json ELSE agiso_orders.raw_json END,
    updated_at = NOW()
`, event.BizOrderID, event.ItemID, raw, service.AgisoOrderStatusPartial)
	if err != nil {
		return nil, err
	}
	return r.GetByBizOrderID(ctx, event.BizOrderID)
}

func (r *agisoOrderRepository) GetByBizOrderID(ctx context.Context, bizOrderID string) (*service.AgisoOrder, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id, biz_order_id, item_id, buyer_id, payment_cents, value, redeem_code_id, code,
       detail_fetched, code_minted, msg_sent, shipped, status, raw_json::text,
       created_at, updated_at, delivered_at
FROM agiso_orders
WHERE biz_order_id = $1
`, bizOrderID)
	order, err := scanAgisoOrder(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrAgisoOrderNotFound
	}
	return order, err
}

func (r *agisoOrderRepository) Update(ctx context.Context, order *service.AgisoOrder) error {
	var redeemID any
	if order.RedeemCodeID != nil {
		redeemID = *order.RedeemCodeID
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE agiso_orders
SET buyer_id = $2,
    payment_cents = $3,
    value = $4,
    redeem_code_id = $5,
    code = $6,
    detail_fetched = $7,
    code_minted = $8,
    msg_sent = $9,
    shipped = $10,
    status = $11,
    delivered_at = $12,
    updated_at = NOW()
WHERE biz_order_id = $1
`, order.BizOrderID, order.BuyerID, order.PaymentCents, order.Value, redeemID, order.Code, order.DetailFetched, order.CodeMinted, order.MsgSent, order.Shipped, order.Status, order.DeliveredAt)
	return err
}

type agisoScanner interface {
	Scan(dest ...any) error
}

func scanAgisoOrder(row agisoScanner) (*service.AgisoOrder, error) {
	var (
		order       service.AgisoOrder
		redeemID    sql.NullInt64
		raw         string
		deliveredAt sql.NullTime
	)
	if err := row.Scan(
		&order.ID,
		&order.BizOrderID,
		&order.ItemID,
		&order.BuyerID,
		&order.PaymentCents,
		&order.Value,
		&redeemID,
		&order.Code,
		&order.DetailFetched,
		&order.CodeMinted,
		&order.MsgSent,
		&order.Shipped,
		&order.Status,
		&raw,
		&order.CreatedAt,
		&order.UpdatedAt,
		&deliveredAt,
	); err != nil {
		return nil, err
	}
	if redeemID.Valid {
		order.RedeemCodeID = &redeemID.Int64
	}
	if deliveredAt.Valid {
		v := deliveredAt.Time
		order.DeliveredAt = &v
	}
	order.RawJSON = raw
	return &order, nil
}

func sanitizeAgisoRawJSON(raw string) string {
	if raw == "" {
		return "{}"
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "{}"
	}
	delete(payload, "sign")
	out, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(out)
}
