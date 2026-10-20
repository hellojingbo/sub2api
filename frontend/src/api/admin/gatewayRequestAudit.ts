import { apiClient } from '../client'

export interface GatewayRequestAuditState {
  active: boolean
  session_id: string
  started_at?: string
  stopped_at?: string
  count: number
  bytes: number
}

export type GatewayRequestHeaders = Record<string, string[]>

export interface GatewayRequestAuditRecord {
  id: string
  captured_at: string
  session_id: string
  transport: string
  account_id?: number
  account_name?: string
  request_id?: string
  downstream_method: string
  downstream_url: string
  downstream_headers: GatewayRequestHeaders
  downstream_body?: string
  downstream_body_codec?: string
  upstream_method: string
  upstream_url: string
  upstream_headers: GatewayRequestHeaders
  upstream_body?: string
  upstream_body_codec?: string
  upstream_wire_body?: string
  upstream_wire_body_codec?: string
}

export async function state(): Promise<GatewayRequestAuditState> {
  const { data } = await apiClient.get('/admin/gateway-request-audit/state')
  return data
}

export async function start(): Promise<GatewayRequestAuditState> {
  const { data } = await apiClient.post('/admin/gateway-request-audit/start')
  return data
}

export async function stop(): Promise<GatewayRequestAuditState> {
  const { data } = await apiClient.post('/admin/gateway-request-audit/stop')
  return data
}

export async function clear(): Promise<{ cleared: boolean }> {
  const { data } = await apiClient.delete('/admin/gateway-request-audit/records')
  return data
}

export async function list(): Promise<GatewayRequestAuditRecord[]> {
  const { data } = await apiClient.get('/admin/gateway-request-audit/records')
  return data
}

export async function get(id: string): Promise<GatewayRequestAuditRecord> {
  const { data } = await apiClient.get(`/admin/gateway-request-audit/records/${encodeURIComponent(id)}`)
  return data
}

export const gatewayRequestAuditAPI = { state, start, stop, clear, list, get }

export default gatewayRequestAuditAPI
