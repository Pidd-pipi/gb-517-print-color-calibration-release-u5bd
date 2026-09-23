
import { request } from './client';
import type { CreateReleaseDecisionInput, ReleaseDecisionRecord } from '../types/domain';

export async function listReleaseDecision(page = 1, pageSize = 20, search = '') {
  return request<ReleaseDecisionRecord[]>(`/release?page=${page}&pageSize=${pageSize}&search=${encodeURIComponent(search)}`);
}
export async function getReleaseDecision(id: number) {
  return request<ReleaseDecisionRecord>(`/release/${id}`);
}
export async function createReleaseDecision(input: CreateReleaseDecisionInput) {
  return request<ReleaseDecisionRecord>('/release', { method: 'POST', body: JSON.stringify(input) });
}
export async function transitionReleaseDecision(id: number, status: string, expectedVersion: number, reason: string) {
  return request<ReleaseDecisionRecord>(`/release/${id}/transition`, {
    method: 'POST', body: JSON.stringify({ status, expectedVersion, reason }),
  });
}
