-- 节点实际额度与平台预占分开保存，未回报的容量保持未知。
ALTER TABLE vpn_servers ADD COLUMN allocation_snapshot JSONB;
