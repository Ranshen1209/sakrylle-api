package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	AgisoOrderStatusPartial   = "partial"
	AgisoOrderStatusDelivered = "delivered"
	AgisoOrderStatusFailed    = "failed"
)

var ErrAgisoOrderNotFound = errors.New("agiso order not found")

type AgisoOrder struct {
	ID            int64
	BizOrderID    string
	ItemID        string
	BuyerID       string
	PaymentCents  int64
	Value         float64
	RedeemCodeID  *int64
	Code          string
	DetailFetched bool
	CodeMinted    bool
	MsgSent       bool
	Shipped       bool
	Status        string
	RawJSON       string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	DeliveredAt   *time.Time
}

type AgisoOrderDetail struct {
	PaymentCents int64
	BuyerID      string
	OrderStatus  int
	RawJSON      string
}

type AgisoOrderRepository interface {
	Ensure(ctx context.Context, event AgisoDeliveryEvent) (*AgisoOrder, error)
	GetByBizOrderID(ctx context.Context, bizOrderID string) (*AgisoOrder, error)
	Update(ctx context.Context, order *AgisoOrder) error
}

type AgisoOutboundClient interface {
	OrderDetail(ctx context.Context, tid string) (AgisoOrderDetail, error)
	SendMsg(ctx context.Context, tid, msg string) error
	DummySend(ctx context.Context, tid string) error
}

type AgisoRedeemMinter interface {
	GenerateRandomCode() (string, error)
	CreateCode(ctx context.Context, code *RedeemCode) error
	GetByID(ctx context.Context, id int64) (*RedeemCode, error)
	GetByCode(ctx context.Context, code string) (*RedeemCode, error)
	BatchUpdate(ctx context.Context, input *RedeemCodeBatchUpdateInput) (*RedeemCodeBatchUpdateResult, error)
}

type AgisoDeliveryService struct {
	cfg    config.AgisoConfig
	repo   AgisoOrderRepository
	client AgisoOutboundClient
	redeem AgisoRedeemMinter
}

func NewAgisoDeliveryService(cfg config.AgisoConfig, repo AgisoOrderRepository, client AgisoOutboundClient, redeem AgisoRedeemMinter) *AgisoDeliveryService {
	if cfg.ValueMultiplier == 0 {
		cfg.ValueMultiplier = 1
	}
	return &AgisoDeliveryService{cfg: cfg, repo: repo, client: client, redeem: redeem}
}

func (s *AgisoDeliveryService) HandleEvent(ctx context.Context, event AgisoDeliveryEvent) error {
	if event.IsRefund() {
		return s.handleRefund(ctx, event)
	}
	if !event.IsPayment() {
		return nil
	}
	if s.cfg.SellerID != "" && event.SellerID != s.cfg.SellerID {
		return nil
	}

	order, err := s.repo.Ensure(ctx, event)
	if err != nil {
		return err
	}
	if order.Status == AgisoOrderStatusDelivered && order.MsgSent && order.Shipped {
		return nil
	}
	if order.Status == "" {
		order.Status = AgisoOrderStatusPartial
	}

	if !order.DetailFetched {
		detail, err := s.client.OrderDetail(ctx, order.BizOrderID)
		if err != nil {
			order.Status = AgisoOrderStatusPartial
			_ = s.repo.Update(ctx, order)
			return err
		}
		if detail.PaymentCents <= 0 {
			order.Status = AgisoOrderStatusFailed
			_ = s.repo.Update(ctx, order)
			return fmt.Errorf("agiso order %s has invalid payment_cents %d", order.BizOrderID, detail.PaymentCents)
		}
		value, err := AgisoRedeemValue(detail.PaymentCents, s.cfg.ValueMultiplier)
		if err != nil {
			order.Status = AgisoOrderStatusFailed
			_ = s.repo.Update(ctx, order)
			return err
		}
		order.PaymentCents = detail.PaymentCents
		order.BuyerID = detail.BuyerID
		order.Value = value
		order.DetailFetched = true
		order.Status = AgisoOrderStatusPartial
		if err := s.repo.Update(ctx, order); err != nil {
			return err
		}
	}

	if !order.CodeMinted {
		if order.Code == "" {
			code, err := s.redeem.GenerateRandomCode()
			if err != nil {
				return err
			}
			order.Code = code
			order.Status = AgisoOrderStatusPartial
			if err := s.repo.Update(ctx, order); err != nil {
				return err
			}
		}
		expiresAt := agisoCodeExpiresAt(s.cfg.CodeExpiresDays)
		redeemCode := &RedeemCode{Code: order.Code, Type: RedeemTypeBalance, Value: order.Value, Status: StatusUnused, Notes: "agiso:biz_order_id=" + order.BizOrderID, ExpiresAt: expiresAt}
		if err := s.redeem.CreateCode(ctx, redeemCode); err != nil {
			existing, lookupErr := s.redeem.GetByCode(ctx, order.Code)
			if lookupErr != nil || existing == nil {
				order.Status = AgisoOrderStatusPartial
				_ = s.repo.Update(ctx, order)
				return err
			}
			redeemCode = existing
		}
		order.RedeemCodeID = &redeemCode.ID
		order.Code = redeemCode.Code
		order.CodeMinted = true
		order.Status = AgisoOrderStatusPartial
		if err := s.repo.Update(ctx, order); err != nil {
			return err
		}
	}

	if !order.MsgSent {
		msg, err := BuildAgisoDeliveryMessage(order.Value, order.Code)
		if err != nil {
			return err
		}
		if err := s.client.SendMsg(ctx, order.BizOrderID, msg); err != nil {
			order.Status = AgisoOrderStatusPartial
			_ = s.repo.Update(ctx, order)
			return err
		}
		order.MsgSent = true
		order.Status = AgisoOrderStatusPartial
		if err := s.repo.Update(ctx, order); err != nil {
			return err
		}
	}

	if !order.Shipped {
		if err := s.client.DummySend(ctx, order.BizOrderID); err != nil {
			order.Status = AgisoOrderStatusPartial
			_ = s.repo.Update(ctx, order)
			return err
		}
		now := time.Now()
		order.Shipped = true
		order.Status = AgisoOrderStatusDelivered
		order.DeliveredAt = &now
		return s.repo.Update(ctx, order)
	}

	return nil
}

func (s *AgisoDeliveryService) handleRefund(ctx context.Context, event AgisoDeliveryEvent) error {
	order, err := s.repo.GetByBizOrderID(ctx, event.BizOrderID)
	if errors.Is(err, ErrAgisoOrderNotFound) {
		return nil
	}
	if err != nil || order == nil || order.RedeemCodeID == nil {
		return err
	}
	code, err := s.redeem.GetByID(ctx, *order.RedeemCodeID)
	if errors.Is(err, ErrRedeemCodeNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if code.Status != StatusUnused {
		return nil
	}
	status := StatusDisabled
	_, err = s.redeem.BatchUpdate(ctx, &RedeemCodeBatchUpdateInput{IDs: []int64{code.ID}, Fields: RedeemCodeBatchUpdateFields{Status: &status}})
	return err
}

func agisoCodeExpiresAt(days int) *time.Time {
	if days <= 0 {
		return nil
	}
	t := time.Now().Add(time.Duration(days) * 24 * time.Hour)
	return &t
}
