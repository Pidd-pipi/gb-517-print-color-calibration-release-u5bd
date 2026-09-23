
export interface DomainRecord {
  id: number;
  code: string;
  name: string;
  status: string;
  version: number;
  description: string;
  facility: string;
  owner: string;
  category: string;
  riskLevel: 'low' | 'medium' | 'high' | 'critical';
  metricValue: number;
  metricUnit: string;
  effectiveAt: string;
  evidence: string;
  relatedCode: string;
  toleranceLimit?: number;
  createdAt: string;
  updatedAt: string;
  revisions?: RevisionRecord[];
}

export interface ReleaseDecisionRecord extends DomainRecord {
  printRunId: number;
  colorProofId: number;
  printRunCode: string;
  colorProofNo: string;
  basisRunVersion: number;
  basisProofVersion: number;
  basisProofReading: number;
  basisToleranceLimit: number;
  basisInvalidReason: string;
  basisValid: boolean;
  basisReason: string;
}

export interface CreateReleaseDecisionInput {
  code?: string;
  description?: string;
  printRunId: number;
  colorProofId: number;
  evidence?: string;
}

export interface RevisionRecord {
  id: number; version: number; status: string; name: string; metricValue: number;
  metricUnit: string; evidence: string; actor: string; requestId: string; reason: string; createdAt: string;
  printRunCode?: string; colorProofNo?: string;
  basisRunVersion?: number; basisProofVersion?: number;
  basisInvalidReason?: string;
}

export interface PageMeta { page: number; pageSize: number; total: number }
export interface ApiEnvelope<T> { data: T; error?: string; message?: string; meta?: PageMeta }
export interface UserSession { token: string; username: string; displayName: string; role: string; expiresIn: number }
export interface AuditLog {
  id: number; requestId: string; actor: string; action: string; entityType: string;
  entityId: number; beforeState: string; afterState: string; detail: string; createdAt: string;
}
export interface EntityConfig { key: string; path: string; label: string; statuses: readonly string[] }
