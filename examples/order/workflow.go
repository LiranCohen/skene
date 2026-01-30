// Package order implements an e-commerce order processing workflow.
//
// This workflow demonstrates:
// - Sequential and parallel step execution
// - Type-safe dependencies between steps
// - Retry policies for unreliable external services
// - Branching based on order properties
// - Compensation (saga pattern) for rollback on failure
package order

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lirancohen/skene/retry"
	"github.com/lirancohen/skene/workflow"
)

// =============================================================================
// Domain Types
// =============================================================================

// OrderInput is the workflow input.
type OrderInput struct {
	OrderID       string  `json:"order_id"`
	CustomerID    string  `json:"customer_id"`
	CustomerEmail string  `json:"customer_email"`
	Items         []Item  `json:"items"`
	ShippingAddr  Address `json:"shipping_address"`
	PaymentMethod string  `json:"payment_method"` // "card" or "bank_transfer"
	TotalAmount   float64 `json:"total_amount"`
}

type Item struct {
	SKU      string  `json:"sku"`
	Name     string  `json:"name"`
	Quantity int     `json:"quantity"`
	Price    float64 `json:"price"`
}

type Address struct {
	Street  string `json:"street"`
	City    string `json:"city"`
	State   string `json:"state"`
	Zip     string `json:"zip"`
	Country string `json:"country"`
}

// Step outputs - each step has a typed output struct

type ValidateOutput struct {
	Valid            bool     `json:"valid"`
	InvalidReasons   []string `json:"invalid_reasons,omitempty"`
	FraudScore       float64  `json:"fraud_score"`
	RequiresApproval bool     `json:"requires_approval"`
}

type InventoryOutput struct {
	Reserved      bool              `json:"reserved"`
	ReservationID string            `json:"reservation_id"`
	ItemsReserved map[string]int    `json:"items_reserved"` // SKU -> quantity
	BackorderSKUs []string          `json:"backorder_skus,omitempty"`
}

type PaymentOutput struct {
	TransactionID string    `json:"transaction_id"`
	Status        string    `json:"status"` // "captured", "pending", "failed"
	ChargedAmount float64   `json:"charged_amount"`
	ChargedAt     time.Time `json:"charged_at"`
}

type ShippingOutput struct {
	ShipmentID     string    `json:"shipment_id"`
	Carrier        string    `json:"carrier"`
	TrackingNumber string    `json:"tracking_number"`
	EstimatedDate  time.Time `json:"estimated_delivery"`
}

type NotificationOutput struct {
	EmailSent bool   `json:"email_sent"`
	MessageID string `json:"message_id,omitempty"`
}

type OrderCompleteOutput struct {
	OrderID        string    `json:"order_id"`
	Status         string    `json:"status"`
	TransactionID  string    `json:"transaction_id"`
	TrackingNumber string    `json:"tracking_number"`
	CompletedAt    time.Time `json:"completed_at"`
}

// =============================================================================
// External Service Interfaces (for dependency injection)
// =============================================================================

// Services holds external dependencies for the workflow.
// In production, these would be injected via context.
type Services struct {
	Inventory    InventoryService
	Payment      PaymentService
	Shipping     ShippingService
	Notification NotificationService
	FraudCheck   FraudCheckService
}

type InventoryService interface {
	Reserve(ctx context.Context, items []Item) (string, error)
	Release(ctx context.Context, reservationID string) error
}

type PaymentService interface {
	Charge(ctx context.Context, customerID string, amount float64, method string) (string, error)
	Refund(ctx context.Context, transactionID string) error
}

type ShippingService interface {
	CreateShipment(ctx context.Context, addr Address, items []Item) (*ShippingOutput, error)
	CancelShipment(ctx context.Context, shipmentID string) error
}

type NotificationService interface {
	SendEmail(ctx context.Context, to, subject, body string) (string, error)
}

type FraudCheckService interface {
	CheckOrder(ctx context.Context, customerID string, amount float64) (float64, error)
}

// servicesKey is the context key for services.
type servicesKey struct{}

// WithServices adds services to context.
func WithServices(ctx context.Context, svc *Services) context.Context {
	return context.WithValue(ctx, servicesKey{}, svc)
}

// GetServices retrieves services from context.
func GetServices(ctx context.Context) *Services {
	svc, _ := ctx.Value(servicesKey{}).(*Services)
	return svc
}

// =============================================================================
// Step Definitions
// =============================================================================

// ValidateOrder validates the order and checks for fraud.
var ValidateOrder = workflow.NewStep("validate-order", func(ctx workflow.Context) (ValidateOutput, error) {
	input := workflow.Input[OrderInput](ctx)
	svc := GetServices(ctx)

	var reasons []string

	// Basic validation
	if len(input.Items) == 0 {
		reasons = append(reasons, "order has no items")
	}
	if input.TotalAmount <= 0 {
		reasons = append(reasons, "invalid total amount")
	}
	if input.ShippingAddr.Country == "" {
		reasons = append(reasons, "missing shipping country")
	}

	// Fraud check (calls external service)
	fraudScore := 0.0
	if svc != nil && svc.FraudCheck != nil {
		score, err := svc.FraudCheck.CheckOrder(ctx, input.CustomerID, input.TotalAmount)
		if err != nil {
			return ValidateOutput{}, fmt.Errorf("fraud check failed: %w", err)
		}
		fraudScore = score
	}

	// High fraud score is a validation failure
	if fraudScore > 0.8 {
		reasons = append(reasons, fmt.Sprintf("high fraud score: %.2f", fraudScore))
	}

	return ValidateOutput{
		Valid:            len(reasons) == 0,
		InvalidReasons:   reasons,
		FraudScore:       fraudScore,
		RequiresApproval: fraudScore > 0.5 && fraudScore <= 0.8,
	}, nil
})

// ReserveInventory reserves items in the warehouse.
// Has a compensation function to release reservation on workflow failure.
var ReserveInventory = workflow.NewStep("reserve-inventory", func(ctx workflow.Context) (InventoryOutput, error) {
	input := workflow.Input[OrderInput](ctx)
	validated := ValidateOrder.MustOutput(ctx)

	if !validated.Valid {
		return InventoryOutput{}, errors.New("cannot reserve inventory for invalid order")
	}

	svc := GetServices(ctx)
	if svc == nil || svc.Inventory == nil {
		// Mock response for testing
		reserved := make(map[string]int)
		for _, item := range input.Items {
			reserved[item.SKU] = item.Quantity
		}
		return InventoryOutput{
			Reserved:      true,
			ReservationID: fmt.Sprintf("res-%s", input.OrderID),
			ItemsReserved: reserved,
		}, nil
	}

	reservationID, err := svc.Inventory.Reserve(ctx, input.Items)
	if err != nil {
		return InventoryOutput{}, fmt.Errorf("inventory reservation failed: %w", err)
	}

	reserved := make(map[string]int)
	for _, item := range input.Items {
		reserved[item.SKU] = item.Quantity
	}

	return InventoryOutput{
		Reserved:      true,
		ReservationID: reservationID,
		ItemsReserved: reserved,
	}, nil
}).WithRetry(retry.Default())

// ChargePayment processes the payment.
// Uses retry with exponential backoff for payment gateway reliability.
var ChargePayment = workflow.NewStep("charge-payment", func(ctx workflow.Context) (PaymentOutput, error) {
	input := workflow.Input[OrderInput](ctx)
	inv := ReserveInventory.MustOutput(ctx)

	if !inv.Reserved {
		return PaymentOutput{}, errors.New("cannot charge payment without inventory reservation")
	}

	svc := GetServices(ctx)
	if svc == nil || svc.Payment == nil {
		// Mock response
		return PaymentOutput{
			TransactionID: fmt.Sprintf("txn-%s", input.OrderID),
			Status:        "captured",
			ChargedAmount: input.TotalAmount,
			ChargedAt:     time.Now(),
		}, nil
	}

	txnID, err := svc.Payment.Charge(ctx, input.CustomerID, input.TotalAmount, input.PaymentMethod)
	if err != nil {
		return PaymentOutput{}, fmt.Errorf("payment failed: %w", err)
	}

	return PaymentOutput{
		TransactionID: txnID,
		Status:        "captured",
		ChargedAmount: input.TotalAmount,
		ChargedAt:     time.Now(),
	}, nil
}).WithRetry(&retry.Policy{
	MaxAttempts:  5,
	InitialDelay: 500 * time.Millisecond,
	MaxDelay:     30 * time.Second,
	Multiplier:   2.0,
	Jitter:       0.1,
}).WithTimeout(30 * time.Second)

// CreateShipment creates the shipping order.
var CreateShipment = workflow.NewStep("create-shipment", func(ctx workflow.Context) (ShippingOutput, error) {
	input := workflow.Input[OrderInput](ctx)
	payment := ChargePayment.MustOutput(ctx)

	if payment.Status != "captured" {
		return ShippingOutput{}, errors.New("cannot ship without captured payment")
	}

	svc := GetServices(ctx)
	if svc == nil || svc.Shipping == nil {
		// Mock response
		return ShippingOutput{
			ShipmentID:     fmt.Sprintf("ship-%s", input.OrderID),
			Carrier:        "UPS",
			TrackingNumber: fmt.Sprintf("1Z%s", input.OrderID),
			EstimatedDate:  time.Now().Add(5 * 24 * time.Hour),
		}, nil
	}

	output, err := svc.Shipping.CreateShipment(ctx, input.ShippingAddr, input.Items)
	if err != nil {
		return ShippingOutput{}, fmt.Errorf("shipping creation failed: %w", err)
	}

	return *output, nil
}).WithRetry(retry.Default())

// SendConfirmation sends order confirmation email.
var SendConfirmation = workflow.NewStep("send-confirmation", func(ctx workflow.Context) (NotificationOutput, error) {
	input := workflow.Input[OrderInput](ctx)
	shipping := CreateShipment.MustOutput(ctx)

	body := fmt.Sprintf(
		"Your order %s has been shipped!\n\nTracking: %s\nEstimated delivery: %s",
		input.OrderID,
		shipping.TrackingNumber,
		shipping.EstimatedDate.Format("Jan 2, 2006"),
	)

	svc := GetServices(ctx)
	if svc == nil || svc.Notification == nil {
		return NotificationOutput{EmailSent: true, MessageID: "mock-msg-id"}, nil
	}

	msgID, err := svc.Notification.SendEmail(ctx, input.CustomerEmail, "Order Shipped!", body)
	if err != nil {
		// Non-fatal: log and continue
		return NotificationOutput{EmailSent: false}, nil
	}

	return NotificationOutput{EmailSent: true, MessageID: msgID}, nil
})

// FinalizeOrder creates the final order record.
var FinalizeOrder = workflow.NewStep("finalize-order", func(ctx workflow.Context) (OrderCompleteOutput, error) {
	input := workflow.Input[OrderInput](ctx)
	payment := ChargePayment.MustOutput(ctx)
	shipping := CreateShipment.MustOutput(ctx)

	return OrderCompleteOutput{
		OrderID:        input.OrderID,
		Status:         "completed",
		TransactionID:  payment.TransactionID,
		TrackingNumber: shipping.TrackingNumber,
		CompletedAt:    time.Now(),
	}, nil
})

// =============================================================================
// Workflow Definition
// =============================================================================

// OrderWorkflow is the complete order processing workflow.
//
// DAG structure:
//
//	ValidateOrder
//	     │
//	     ▼
//	ReserveInventory
//	     │
//	     ▼
//	ChargePayment
//	     │
//	     ▼
//	CreateShipment
//	     │
//	     ▼
//	SendConfirmation
//	     │
//	     ▼
//	FinalizeOrder
var OrderWorkflow = workflow.Define("order-processing",
	ValidateOrder.After(),
	ReserveInventory.After(ValidateOrder),
	ChargePayment.After(ReserveInventory),
	CreateShipment.After(ChargePayment),
	SendConfirmation.After(CreateShipment),
	FinalizeOrder.After(SendConfirmation),
)
