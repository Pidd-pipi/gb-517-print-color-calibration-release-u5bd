import { useEffect, useMemo, useState } from 'react';
import { useAuth, roleAtLeast } from '../hooks/useAuth';
import { usePagination } from '../hooks/usePagination';
import type { DomainRecord, ReleaseDecisionRecord } from '../types/domain';
import { useReleaseDecisionStore } from '../stores/release-decision';
import { formatDate } from '../utils/format';
import { StatusBadge } from '../components/common/StatusBadge';
import { RunStateBadge } from '../components/common/RunStateBadge';
import { ColorTable } from '../components/common/ColorTable';
import { EmptyState } from '../components/common/EmptyState';
import { MetricCard } from '../components/common/MetricCard';
import { ConfirmDialog } from '../components/common/ConfirmDialog';
import { UiButton } from '../components/common/UiButton';

function decisionRunState(status: string): 'setup' | 'printing' | 'proofing' | 'hold' | 'released' {
  if (status === 'release') return 'released';
  if (status === 'rework' || status === 'quarantine') return 'hold';
  return 'proofing';
}

export default function ReleaseDecisionPage() {
  const { session } = useAuth();
  const { items, meta, loading, error, candidateRuns, candidateProofs, load, loadCandidates, loadDetail, createDraft, transition, clearError } = useReleaseDecisionStore();
  const [search, setSearch] = useState('');
  const [submittedSearch, setSubmittedSearch] = useState('');
  const [showCreate, setShowCreate] = useState(false);
  const [selectedRunId, setSelectedRunId] = useState<number>(0);
  const [selectedProofId, setSelectedProofId] = useState<number>(0);
  const [formError, setFormError] = useState('');
  const [pending, setPending] = useState<{ item: ReleaseDecisionRecord; status: string } | null>(null);
  const [detail, setDetail] = useState<ReleaseDecisionRecord | null>(null);
  const [actionError, setActionError] = useState('');
  const { page, pageSize, pages, setPage, previous, next } = usePagination(meta.total);
  const canWrite = roleAtLeast(session?.role, 'operator');
  const canReview = roleAtLeast(session?.role, 'reviewer');

  useEffect(() => { void load(submittedSearch, page, pageSize); }, [load, page, pageSize, submittedSearch]);

  const validBasisCount = useMemo(() => items.filter((item) => item.basisValid).length, [items]);
  const invalidBasisCount = items.length - validBasisCount;

  const openCreate = async () => {
    setFormError('');
    setSelectedRunId(0);
    setSelectedProofId(0);
    await loadCandidates();
    setShowCreate(true);
  };

  const selectedRun = candidateRuns.find((run) => run.id === selectedRunId) || null;
  const selectedProof = candidateProofs.find((proof) => proof.id === selectedProofId) || null;

  const submitCreate = async () => {
    setFormError('');
    if (!selectedRunId || !selectedProofId) {
      setFormError('必须选择一条校样阶段的印刷批次和一份已接收校样。');
      return;
    }
    try {
      await createDraft({ printRunId: selectedRunId, colorProofId: selectedProofId });
      setShowCreate(false);
    } catch (caught) {
      setFormError(caught instanceof Error ? caught.message : String(caught));
    }
  };

  const openDetail = async (item: ReleaseDecisionRecord) => {
    try { setDetail(await loadDetail(item.id)); } catch { setDetail(item); }
  };

  const confirmTransition = async () => {
    if (!pending) return;
    setActionError('');
    try {
      await transition(pending.item, pending.status);
      setPending(null);
    } catch (caught) {
      setActionError(caught instanceof Error ? caught.message : String(caught));
    }
  };

  const renderBasisTag = (item: ReleaseDecisionRecord) => item.basisValid
    ? <span className="basis-tag basis-tag--valid">依据有效</span>
    : <span className="basis-tag basis-tag--invalid" title={item.basisReason}>依据失效</span>;

  return <main className="workspace">
    <header className="page-header">
      <div><p className="eyebrow">业务工作台</p><h1>放行决定</h1><p>每条放行决定必须关联一条印刷批次和一份已接收校样；点放行时会重新读取两条记录。</p></div>
      {canWrite && <UiButton onClick={() => void openCreate()}>新增放行草稿</UiButton>}
    </header>
    <section className="metrics">
      <MetricCard label="决定总数" value={meta.total} detail="当前筛选范围" />
      <MetricCard label="依据有效" value={validBasisCount} detail="批次校样阶段且读数合格" />
      <MetricCard label="依据失效" value={invalidBasisCount} detail="已拦住放行，需要重新建草稿" />
    </section>
    <ColorTable records={items} title="放行依据读数" />
    <section className="toolbar">
      <input aria-label="搜索" placeholder="搜索放行决定、关联批次或校样编号" value={search} onChange={(event) => setSearch(event.target.value)} />
      <UiButton onClick={() => { setPage(1); setSubmittedSearch(search); }}>查询</UiButton>
      <button className="link-button" onClick={() => { setSearch(''); setSubmittedSearch(''); setPage(1); }}>重置</button>
    </section>
    {error && <div className="alert" role="alert">{error}<button className="link-button" onClick={clearError}>关闭</button></div>}
    <section className="table-shell" aria-busy={loading}>
      <table>
        <thead><tr><th>决定编号</th><th>关联批次 / 校样</th><th>决定状态</th><th>依据</th><th>依据读数</th><th>更新时间</th><th>操作</th></tr></thead>
        <tbody>
          {items.map((item) => {
            const releaseBlocked = item.status === 'draft' && !item.basisValid;
            return <tr key={item.id}>
              <td><strong>{item.code}</strong><small>{item.name}</small></td>
              <td><strong>{item.printRunCode}</strong><small>校样 {item.colorProofNo} · v{item.basisProofVersion}</small></td>
              <td><StatusBadge status={item.status} /> <RunStateBadge state={decisionRunState(item.status)} /></td>
              <td>{renderBasisTag(item)}<small className={item.basisValid ? 'basis-reason basis-reason--valid' : 'basis-reason'}>{item.basisReason}</small></td>
              <td>{item.basisProofReading} {item.metricUnit}<small>允许 ≤ {item.basisToleranceLimit}</small></td>
              <td>{formatDate(item.updatedAt)}</td>
              <td>
                {item.status === 'draft' && canWrite && (canReview || !releaseBlocked)
                  ? <button className="table-action" disabled={releaseBlocked} title={releaseBlocked ? item.basisReason : '复核放行'} onClick={() => setPending({ item, status: 'release' })}>放行</button>
                  : null}
                {item.status === 'draft' && canWrite && <button className="table-action" onClick={() => setPending({ item, status: 'rework' })}>返修</button>}
                <button className="table-action" onClick={() => void openDetail(item)}>查看详情</button>
              </td>
            </tr>;
          })}
          {!items.length && !loading && <tr><td colSpan={7}><EmptyState title="没有匹配记录" detail="可清空搜索条件后重新查询" /></td></tr>}
        </tbody>
      </table>
      {loading && <div className="loading">正在同步放行依据…</div>}
    </section>
    <footer className="pagination"><button onClick={previous} disabled={page <= 1}>上一页</button><span>第 {page} / {pages} 页</span><button onClick={next} disabled={page >= pages}>下一页</button></footer>

    <ConfirmDialog open={showCreate} title="新增放行草稿" onCancel={() => setShowCreate(false)} onConfirm={() => void submitCreate()}>
      <p>只有批次处于<strong>校样阶段</strong>且校样为<strong>已接收</strong>、读数未超出允许范围时，才会生成草稿。</p>
      <label className="basis-field">印刷批次（校样阶段）
        <select value={selectedRunId} onChange={(event) => setSelectedRunId(Number(event.target.value))}>
          <option value={0}>请选择印刷批次</option>
          {candidateRuns.map((run: DomainRecord) => <option key={run.id} value={run.id}>{run.code} · {run.name}（{run.status}，允许 ΔE ≤ {run.toleranceLimit ?? 3}）</option>)}
        </select>
      </label>
      <label className="basis-field">已接收校样
        <select value={selectedProofId} onChange={(event) => setSelectedProofId(Number(event.target.value))}>
          <option value={0}>请选择色彩校样</option>
          {candidateProofs.map((proof: DomainRecord) => <option key={proof.id} value={proof.id}>{proof.code} · {proof.name}（{proof.status}，读数 {proof.metricValue} {proof.metricUnit}）</option>)}
        </select>
      </label>
      {selectedRun && selectedProof && <div className="basis-preview">
        <span>批次 {selectedRun.code} 当前 {selectedRun.status}</span>
        <span>校样 {selectedProof.code} 当前 {selectedProof.status}，读数 {selectedProof.metricValue} {selectedProof.metricUnit}</span>
      </div>}
      {formError && <div className="alert" role="alert">{formError}</div>}
    </ConfirmDialog>

    <ConfirmDialog open={Boolean(pending)} title={pending?.status === 'release' ? '确认放行' : '确认返修'} onCancel={() => { setPending(null); setActionError(''); }} onConfirm={() => void confirmTransition()}>
      {pending && <div className="detail-content">
        <p>决定 {pending.item.code} 关联批次 <strong>{pending.item.printRunCode}</strong> 与校样 <strong>{pending.item.colorProofNo}</strong>。</p>
        {pending.status === 'release' && <p>放行时会在同一事务内重新读取两条记录；依据一旦失效将自动标记并拦住本次放行。</p>}
        {renderBasisTag(pending.item)}
        {pending.status === 'release' && !pending.item.basisValid && <div className="alert">失效原因：{pending.item.basisReason}</div>}
        {actionError && <div className="alert" role="alert">{actionError}</div>}
      </div>}
    </ConfirmDialog>

    <ConfirmDialog open={Boolean(detail)} title={`${detail?.code || ''} 放行决定详情`} onCancel={() => setDetail(null)} onConfirm={() => setDetail(null)}>
      {detail && <div className="detail-content">
        <p>{detail.description || '无补充说明'}</p>
        <dl>
          <div><dt>关联批次</dt><dd><strong>{detail.printRunCode}</strong>（依据快照 v{detail.basisRunVersion}）</dd></div>
          <div><dt>关联校样</dt><dd><strong>{detail.colorProofNo}</strong>（依据快照 v{detail.basisProofVersion}）</dd></div>
          <div><dt>依据状态</dt><dd>{renderBasisTag(detail)}<small>{detail.basisReason}</small></dd></div>
          <div><dt>快照读数</dt><dd>{detail.basisProofReading} {detail.metricUnit}（允许 ≤ {detail.basisToleranceLimit}）</dd></div>
          <div><dt>当前版本</dt><dd>v{detail.version}</dd></div>
          <div><dt>证据</dt><dd>{detail.evidence || '-'}</dd></div>
        </dl>
        <ColorTable records={[detail]} title="决定依据色彩读数" />
        {detail.revisions?.length ? <div className="revision-list"><h3>版本链（含依据失效留痕）</h3>{detail.revisions.map((revision) => <article key={revision.id}>
          <strong>v{revision.version} · {revision.status}</strong>
          {revision.basisInvalidReason && <small className="basis-revision-invalid">依据失效：{revision.basisInvalidReason}</small>}
          <span>{revision.actor} · {revision.reason}</span><code>{revision.requestId}</code>
        </article>)}</div> : null}
      </div>}
    </ConfirmDialog>
  </main>;
}
