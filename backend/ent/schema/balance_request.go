package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// BalanceRequest 用户提交的余额申请单。
//
// 业务背景：PunkcodeAI 桌面端不直接走支付通道，余额由管理员审批后充值。
// 流程：
//  1. 用户在桌面端点击 "申请充值" → POST /api/v1/cli/balance-requests
//  2. 管理员在 sub2api 后台审批列表查看 → /api/v1/admin/balance-requests
//  3. 管理员同意（可改金额）→ 直接 UPDATE users SET balance = balance + N
//  4. 或拒绝（带 reject_reason）
//
// 注：本 ent schema 仅用于未来 `go generate ./ent` 时生成 client 代码。
// M3 阶段 repository 走原生 SQL（避免 ent generate 工具链依赖）。
type BalanceRequest struct {
	ent.Schema
}

func (BalanceRequest) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "balance_requests"},
	}
}

func (BalanceRequest) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (BalanceRequest) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.Float("amount_usd").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Positive().
			Comment("申请金额（USD）"),
		field.Float("approved_amount_usd").
			SchemaType(map[string]string{dialect.Postgres: "decimal(20,8)"}).
			Optional().
			Nillable().
			Comment("批准金额（USD），可与 amount_usd 不同；rejected 时为空"),
		field.String("note").
			MaxLen(1000).
			Default("").
			Comment("用户备注"),
		field.String("status").
			MaxLen(20).
			Default("pending").
			Comment("pending / approved / rejected"),
		field.Int64("reviewer_id").
			Optional().
			Nillable().
			Comment("审批人 user id"),
		field.Time("reviewed_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}).
			Comment("审批时间"),
		field.String("reject_reason").
			MaxLen(500).
			Default("").
			Comment("拒绝原因"),
	}
}

func (BalanceRequest) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "status"),
		index.Fields("status", "created_at"),
	}
}
