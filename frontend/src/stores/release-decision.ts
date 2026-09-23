
import { create } from 'zustand';
import {
  createReleaseDecision, getReleaseDecision, listReleaseDecision, transitionReleaseDecision,
} from '../api/release-decision';
import { listColorProof } from '../api/color-proof';
import { listPrintRun } from '../api/print-run';
import type {
  CreateReleaseDecisionInput, DomainRecord, PageMeta, ReleaseDecisionRecord,
} from '../types/domain';

interface ReleaseDecisionState {
  items: ReleaseDecisionRecord[];
  meta: PageMeta;
  loading: boolean;
  error: string;
  candidateRuns: DomainRecord[];
  candidateProofs: DomainRecord[];
  load: (search?: string, page?: number, pageSize?: number) => Promise<void>;
  loadDetail: (id: number) => Promise<ReleaseDecisionRecord>;
  loadCandidates: () => Promise<void>;
  createDraft: (input: CreateReleaseDecisionInput) => Promise<void>;
  transition: (item: ReleaseDecisionRecord, status: string, reason?: string) => Promise<void>;
  clearError: () => void;
}

export const useReleaseDecisionStore = create<ReleaseDecisionState>((set, get) => ({
  items: [],
  meta: { page: 1, pageSize: 20, total: 0 },
  loading: false,
  error: '',
  candidateRuns: [],
  candidateProofs: [],
  load: async (search = '', page = 1, pageSize = 20) => {
    set({ loading: true, error: '' });
    try {
      const result = await listReleaseDecision(page, pageSize, search);
      set({
        items: result.data,
        meta: result.meta || { page: 1, pageSize: 20, total: result.data.length },
        loading: false,
      });
    } catch (error) {
      set({ error: error instanceof Error ? error.message : String(error), loading: false });
    }
  },
  loadDetail: async (id) => getReleaseDecision(id).then((result) => result.data),
  loadCandidates: async () => {
    try {
      const [runs, proofs] = await Promise.all([
        listPrintRun(1, 100),
        listColorProof(1, 100),
      ]);
      set({
        candidateRuns: runs.data,
        candidateProofs: proofs.data,
      });
    } catch (error) {
      set({ error: error instanceof Error ? error.message : String(error) });
    }
  },
  createDraft: async (input) => {
    set({ loading: true, error: '' });
    try {
      await createReleaseDecision(input);
      await get().load();
    } catch (error) {
      set({ error: error instanceof Error ? error.message : String(error), loading: false });
      throw error;
    }
  },
  transition: async (item, status, reason = '前端工作台人工确认') => {
    set({ loading: true, error: '' });
    try {
      await transitionReleaseDecision(item.id, status, item.version, reason);
      await get().load();
    } catch (error) {
      // Re-read after a blocked release so the persisted invalidation reason
      // shows immediately; the error is still surfaced to the action dialog.
      await get().load();
      set({ error: error instanceof Error ? error.message : String(error), loading: false });
      throw error;
    }
  },
  clearError: () => set({ error: '' }),
}));
