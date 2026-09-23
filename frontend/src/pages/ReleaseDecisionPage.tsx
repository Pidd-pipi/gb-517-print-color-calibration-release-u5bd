import { useEffect, useMemo, useState } from 'react';
import { EntityPage } from '../components/EntityPage';
import { ENTITY_CONFIGS } from '../types/status';
import { useReleaseDecisionStore } from '../stores/release-decision';
import { request } from '../api/client';
import type { DomainRecord } from '../types/domain';

function findEligibleBasis(runs: DomainRecord[], proofs: DomainRecord[]) {
  for (const run of runs) {
    if (run.status !== 'proofing') continue;
    const proof = proofs.find((item) =>
      item.status === 'accepted' &&
      item.relatedCode.trim().toLowerCase() === run.relatedCode.trim().toLowerCase() &&
      (run.allowedMax || 0) > 0 && item.metricValue >= (run.allowedMin || 0) && item.metricValue <= (run.allowedMax || 0));
    if (proof) return { run, proof };
  }
  return { run: runs[0], proof: proofs[0] };
}

export default function ReleaseDecisionPage() {
  const [runs, setRuns] = useState<DomainRecord[]>([]);
  const [proofs, setProofs] = useState<DomainRecord[]>([]);
  const defaultBasis = useMemo(() => findEligibleBasis(runs, proofs), [runs, proofs]);

  useEffect(() => {
    void Promise.all([
      request<DomainRecord[]>('/runs?page=1&pageSize=100'),
      request<DomainRecord[]>('/proofs?page=1&pageSize=100'),
    ]).then(([runPage, proofPage]) => {
      setRuns(runPage.data);
      setProofs(proofPage.data);
    }).catch(() => undefined);
  }, []);

  const buildDraft = (): Partial<DomainRecord> => {
    const now = Date.now();
    const run = defaultBasis.run;
    const proof = defaultBasis.proof;
    return {
      code: `RELEASEDECISION-${now.toString().slice(-6)}`,
      name: '新增放行决定',
      description: '基于已接收校样创建的放行草稿',
      facility: run?.facility || '默认作业区',
      owner: 'reviewer',
      category: run?.category || '常规',
      riskLevel: proof?.riskLevel || run?.riskLevel || 'medium',
      metricValue: proof?.metricValue,
      metricUnit: proof?.metricUnit || run?.metricUnit || 'ΔE',
      effectiveAt: new Date().toISOString(),
      evidence: proof?.evidence || '已复核关联批次与校样',
      relatedCode: run?.relatedCode || proof?.relatedCode || '',
      printRunId: run?.id,
      colorProofId: proof?.id,
    };
  };

  return <EntityPage
    config={ENTITY_CONFIGS[3]}
    useStore={useReleaseDecisionStore}
    runs={runs}
    proofs={proofs}
    buildReleaseDraft={buildDraft}
  />;
}
