import React from 'react';
import { render, screen } from '@testing-library/react';
import { OverviewContent } from './OverviewContent';
import { Report } from '../../api/types';

const report: Report = {
  generated_at: '2026-07-14T12:00:00Z',
  argus_version: 'test',
  spec_version: 'e6ee22274284',
  window: '1m0s',
  rule_set_complete: false,
  notes: ['poller verification incomplete: boom'],
  finding_counts: { 'MET-002': 2 },
  snapshot: {
    fleet_score: 84.7,
    services: [
      { service: 'checkout', spec_score: 100, category: 'Excellent', findings: [] },
      {
        service: 'cart',
        spec_score: 76.9,
        category: 'Good',
        extension_score: 73.7,
        findings: [
          {
            rule_id: 'MET-002',
            rule_name: 'metrics have a unit',
            source: 'spec',
            service: 'cart',
            impact: 'important',
            description: 'd',
            confidence: 'sampled',
            stats: { observed: 10, violations: 10, ratio: 1 },
          },
        ],
      },
    ],
    rules_evaluated: ['MET-002', 'RES-005'],
  },
};

const mockGet = jest.fn();
jest.mock('@grafana/runtime', () => ({
  ...jest.requireActual('@grafana/runtime'),
  getBackendSrv: () => ({ get: mockGet }),
}));

describe('OverviewContent', () => {
  beforeEach(() => {
    mockGet.mockReset();
  });

  it('shows the fleet score, service rows, and honest disclosures', async () => {
    mockGet.mockResolvedValue(report);
    render(<OverviewContent />);

    expect(await screen.findByText('84.7')).toBeInTheDocument();
    expect(screen.getByText('checkout')).toBeInTheDocument();
    expect(screen.getByText('cart')).toBeInTheDocument();
    // spec-mandated incomplete-rule-set disclosure
    expect(screen.getByText(/does not yet implement the full rule set/i)).toBeInTheDocument();
    // degradation notes surface, never hidden
    expect(screen.getByText(/poller verification incomplete/i)).toBeInTheDocument();
  });

  it('shows the engine error instead of pretending', async () => {
    mockGet.mockRejectedValue(new Error('engine unreachable: connection refused'));
    render(<OverviewContent />);
    expect(await screen.findByText(/engine unreachable/i)).toBeInTheDocument();
  });
});
