import React from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { ScoresContent } from './ScoresContent';
import { Remediation, Report } from '../../api/types';

const report: Report = {
  generated_at: '2026-07-14T12:00:00Z',
  argus_version: 'test',
  spec_version: 'e6ee22274284',
  window: '1m0s',
  rule_set_complete: false,
  snapshot: {
    fleet_score: 84.7,
    services: [
      {
        service: 'cart',
        spec_score: 76.9,
        category: 'Good',
        findings: [
          {
            rule_id: 'MET-001',
            rule_name: 'bounded metric attribute cardinality',
            source: 'spec',
            service: 'cart',
            impact: 'important',
            description: 'Attribute cardinality must stay bounded.',
            confidence: 'verified',
            stats: { observed: 1, violations: 1, ratio: 1 },
            evidence: [{ kind: 'aggregate', attrs: { metric: 'cart_items', attribute: 'user_id' } }],
          },
        ],
      },
    ],
    rules_evaluated: ['MET-001'],
  },
};

const remediation: Remediation = {
  rule_id: 'MET-001',
  service: 'cart',
  template: 'high-cardinality-attribute',
  formats: {
    'alloy.river': '// review before applying\npatch river',
    'collector.yaml': '# review before applying\npatch yaml',
  },
};

const mockGet = jest.fn();
jest.mock('@grafana/runtime', () => ({
  ...jest.requireActual('@grafana/runtime'),
  getBackendSrv: () => ({ get: mockGet }),
}));

describe('ScoresContent', () => {
  beforeEach(() => {
    mockGet.mockReset();
    mockGet.mockImplementation((url: string) =>
      url.endsWith('/scores') ? Promise.resolve(report) : Promise.resolve(remediation)
    );
  });

  it('drills from service into findings with a confidence badge', async () => {
    render(<ScoresContent />);
    expect(await screen.findByText('bounded metric attribute cardinality')).toBeInTheDocument();
    // confidence badge — verified findings say so explicitly
    expect(screen.getByText('verified')).toBeInTheDocument();
    expect(screen.getByText(/MET-001/)).toBeInTheDocument();
  });

  it('opens the remediation panel with the patch and a copy button', async () => {
    render(<ScoresContent />);
    await userEvent.click(await screen.findByRole('button', { name: /view remediation/i }));
    expect(await screen.findByText(/patch river/)).toBeInTheDocument();
    // one copy button per output format (alloy.river + collector.yaml)
    expect(screen.getAllByRole('button', { name: /copy/i })).toHaveLength(2);
  });
});
