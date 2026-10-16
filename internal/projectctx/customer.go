package projectctx

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Customer represents a real person interacting with digital employees.
// Tracked across channels (feishu, dingtalk, web) by Channel + ExternalID.
type Customer struct {
	ID           uint           `gorm:"primarykey" json:"id"`
	TenantID     string         `gorm:"index;size:100" json:"tenant_id"`
	Channel      string         `gorm:"index;size:50" json:"channel"`
	ExternalID   string         `gorm:"index;size:200" json:"external_id"`
	Name         string         `gorm:"size:200" json:"name"`
	Tags         string         `gorm:"size:500" json:"tags"`
	ExternalRefs string         `gorm:"type:text" json:"external_refs,omitempty"` // JSON: {"crm_id":"xxx","erp_id":"yyy"}
	VisitCount   int            `gorm:"default:0" json:"visit_count"`
	LastSeen     time.Time      `json:"last_seen"`
	FirstSeen    time.Time      `json:"first_seen"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

// FindOrCreateCustomer looks up an existing customer or creates a new one.
func FindOrCreateCustomer(db *gorm.DB, tenantID, channel, externalID, name, externalRefs string) (*Customer, error) {
	var c Customer
	err := db.Where("tenant_id = ? AND channel = ? AND external_id = ?", tenantID, channel, externalID).First(&c).Error
	if err == nil {
		// Update visit info
		c.VisitCount++
		c.LastSeen = time.Now()
		if name != "" && c.Name == "" {
			c.Name = name
		}
		db.Save(&c)
		return &c, nil
	}
	c = Customer{
		TenantID: tenantID, Channel: channel, ExternalID: externalID,
		Name: name, ExternalRefs: externalRefs, VisitCount: 1, LastSeen: time.Now(), FirstSeen: time.Now(),
	}
	db.Create(&c)
	return &c, nil
}

// InjectCustomerIntoContext adds customer info to the context for DE tools.
func InjectCustomerIntoContext(ctx context.Context, customer *Customer) context.Context {
	if customer == nil {
		return ctx
	}
	ctx = context.WithValue(ctx, "customer_id", customer.ID)
	ctx = context.WithValue(ctx, "customer_name", customer.Name)
	ctx = context.WithValue(ctx, "customer_tags", customer.Tags)
	ctx = context.WithValue(ctx, "customer_visits", customer.VisitCount)
	return ctx
}
