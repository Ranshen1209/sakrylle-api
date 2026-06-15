package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAgisoDeliveryService_DeliversOnceAndIgnoresDuplicatePush(t *testing.T) {
	ctx := context.Background()
	repo := newFakeAgisoOrderRepo()
	client := &fakeAgisoClient{detail: AgisoOrderDetail{PaymentCents: 1000, BuyerID: "buyer-1", OrderStatus: 2}}
	redeem := &fakeAgisoRedeemMinter{}
	svc := NewAgisoDeliveryService(config.AgisoConfig{ValueMultiplier: 1}, repo, client, redeem)
	event := AgisoDeliveryEvent{FromPlatform: AgisoPlatformAldsIdle, Aopic: AgisoAopicTradePay, BizOrderID: "1001", ItemID: "2001", OrderStatus: 2, SellerID: "3001", RawJSON: `{"biz_order_id":1001}`}

	require.NoError(t, svc.HandleEvent(ctx, event))
	require.NoError(t, svc.HandleEvent(ctx, event))

	require.Equal(t, 1, client.detailCalls)
	require.Equal(t, 1, client.msgCalls)
	require.Equal(t, 1, client.shipCalls)
	require.Len(t, redeem.created, 1)
	order := repo.mustGet("1001")
	require.Equal(t, AgisoOrderStatusDelivered, order.Status)
	require.True(t, order.DetailFetched)
	require.True(t, order.CodeMinted)
	require.True(t, order.MsgSent)
	require.True(t, order.Shipped)
	require.Equal(t, int64(1000), order.PaymentCents)
	require.Equal(t, 10.0, order.Value)
}

func TestAgisoDeliveryService_ResumesPartialAfterSendMsgFailure(t *testing.T) {
	ctx := context.Background()
	repo := newFakeAgisoOrderRepo()
	client := &fakeAgisoClient{detail: AgisoOrderDetail{PaymentCents: 2500, BuyerID: "buyer-1", OrderStatus: 2}, sendErr: errors.New("send failed")}
	redeem := &fakeAgisoRedeemMinter{}
	svc := NewAgisoDeliveryService(config.AgisoConfig{ValueMultiplier: 1}, repo, client, redeem)
	event := AgisoDeliveryEvent{FromPlatform: AgisoPlatformAldsIdle, Aopic: AgisoAopicTradePay, BizOrderID: "1002", OrderStatus: 2, RawJSON: `{"biz_order_id":1002}`}

	require.Error(t, svc.HandleEvent(ctx, event))
	order := repo.mustGet("1002")
	require.True(t, order.DetailFetched)
	require.True(t, order.CodeMinted)
	require.False(t, order.MsgSent)
	require.Equal(t, AgisoOrderStatusPartial, order.Status)

	client.sendErr = nil
	require.NoError(t, svc.HandleEvent(ctx, event))
	require.Equal(t, 1, client.detailCalls)
	require.Len(t, redeem.created, 1)
	require.Equal(t, 2, client.msgCalls)
	require.Equal(t, 1, client.shipCalls)
}

func TestAgisoDeliveryService_FailClosedWhenPaymentInvalid(t *testing.T) {
	ctx := context.Background()
	repo := newFakeAgisoOrderRepo()
	client := &fakeAgisoClient{detail: AgisoOrderDetail{PaymentCents: 0, BuyerID: "buyer-1", OrderStatus: 2}}
	redeem := &fakeAgisoRedeemMinter{}
	svc := NewAgisoDeliveryService(config.AgisoConfig{ValueMultiplier: 1}, repo, client, redeem)
	event := AgisoDeliveryEvent{FromPlatform: AgisoPlatformAldsIdle, Aopic: AgisoAopicTradePay, BizOrderID: "1003", OrderStatus: 2, RawJSON: `{"biz_order_id":1003}`}

	require.Error(t, svc.HandleEvent(ctx, event))
	require.Len(t, redeem.created, 0)
	require.Equal(t, 0, client.msgCalls)
	require.Equal(t, AgisoOrderStatusFailed, repo.mustGet("1003").Status)
}

func TestAgisoDeliveryService_RefundDisablesUnusedCode(t *testing.T) {
	ctx := context.Background()
	repo := newFakeAgisoOrderRepo()
	repo.orders["1004"] = &AgisoOrder{BizOrderID: "1004", RedeemCodeID: ptrInt64(42), CodeMinted: true, Code: "CODE-1"}
	redeem := &fakeAgisoRedeemMinter{codes: map[int64]*RedeemCode{42: {ID: 42, Code: "CODE-1", Status: StatusUnused}}}
	svc := NewAgisoDeliveryService(config.AgisoConfig{ValueMultiplier: 1}, repo, &fakeAgisoClient{}, redeem)

	event := AgisoDeliveryEvent{FromPlatform: AgisoPlatformAldsIdle, Aopic: AgisoAopicRefund, BizOrderID: "1004", RawJSON: `{"biz_order_id":1004}`}
	require.NoError(t, svc.HandleEvent(ctx, event))
	require.Equal(t, StatusDisabled, redeem.codes[42].Status)
}

type fakeAgisoClient struct {
	detail     AgisoOrderDetail
	detailErr  error
	sendErr    error
	shipErr    error
	detailCalls int
	msgCalls    int
	shipCalls   int
}

func (f *fakeAgisoClient) OrderDetail(_ context.Context, tid string) (AgisoOrderDetail, error) {
	f.detailCalls++
	return f.detail, f.detailErr
}

func (f *fakeAgisoClient) SendMsg(_ context.Context, tid, msg string) error {
	f.msgCalls++
	return f.sendErr
}

func (f *fakeAgisoClient) DummySend(_ context.Context, tid string) error {
	f.shipCalls++
	return f.shipErr
}

type fakeAgisoRedeemMinter struct {
	created []RedeemCode
	codes   map[int64]*RedeemCode
}

func (f *fakeAgisoRedeemMinter) GenerateRandomCode() (string, error) {
	return "AGISO-CODE-0001", nil
}

func (f *fakeAgisoRedeemMinter) CreateCode(_ context.Context, code *RedeemCode) error {
	code.ID = int64(len(f.created) + 1)
	f.created = append(f.created, *code)
	return nil
}

func (f *fakeAgisoRedeemMinter) GetByID(_ context.Context, id int64) (*RedeemCode, error) {
	if f.codes != nil && f.codes[id] != nil {
		cp := *f.codes[id]
		return &cp, nil
	}
	return nil, ErrRedeemCodeNotFound
}

func (f *fakeAgisoRedeemMinter) GetByCode(_ context.Context, code string) (*RedeemCode, error) {
	for _, c := range f.codes {
		if c.Code == code {
			cp := *c
			return &cp, nil
		}
	}
	return nil, ErrRedeemCodeNotFound
}

func (f *fakeAgisoRedeemMinter) BatchUpdate(_ context.Context, input *RedeemCodeBatchUpdateInput) (*RedeemCodeBatchUpdateResult, error) {
	if f.codes == nil || len(input.IDs) != 1 || f.codes[input.IDs[0]] == nil || input.Fields.Status == nil {
		return &RedeemCodeBatchUpdateResult{}, nil
	}
	f.codes[input.IDs[0]].Status = *input.Fields.Status
	return &RedeemCodeBatchUpdateResult{Updated: 1}, nil
}

type fakeAgisoOrderRepo struct {
	orders map[string]*AgisoOrder
}

func newFakeAgisoOrderRepo() *fakeAgisoOrderRepo {
	return &fakeAgisoOrderRepo{orders: map[string]*AgisoOrder{}}
}

func (f *fakeAgisoOrderRepo) Ensure(ctx context.Context, event AgisoDeliveryEvent) (*AgisoOrder, error) {
	if existing := f.orders[event.BizOrderID]; existing != nil {
		return cloneAgisoOrder(existing), nil
	}
	o := &AgisoOrder{BizOrderID: event.BizOrderID, ItemID: event.ItemID, RawJSON: event.RawJSON, Status: AgisoOrderStatusPartial, CreatedAt: time.Now()}
	f.orders[event.BizOrderID] = cloneAgisoOrder(o)
	return cloneAgisoOrder(o), nil
}

func (f *fakeAgisoOrderRepo) GetByBizOrderID(ctx context.Context, bizOrderID string) (*AgisoOrder, error) {
	if o := f.orders[bizOrderID]; o != nil {
		return cloneAgisoOrder(o), nil
	}
	return nil, ErrAgisoOrderNotFound
}

func (f *fakeAgisoOrderRepo) Update(ctx context.Context, order *AgisoOrder) error {
	f.orders[order.BizOrderID] = cloneAgisoOrder(order)
	return nil
}

func (f *fakeAgisoOrderRepo) mustGet(id string) *AgisoOrder {
	return cloneAgisoOrder(f.orders[id])
}

func cloneAgisoOrder(o *AgisoOrder) *AgisoOrder {
	if o == nil {
		return nil
	}
	cp := *o
	if o.RedeemCodeID != nil {
		v := *o.RedeemCodeID
		cp.RedeemCodeID = &v
	}
	return &cp
}

func ptrInt64(v int64) *int64 { return &v }
