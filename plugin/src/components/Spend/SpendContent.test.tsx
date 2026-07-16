import React from 'react';
import { render, screen } from '@testing-library/react';
import { SpendContent } from './SpendContent';
import { Showback } from '../../api/types';

const showback: Showback = {
  generated_at: '2026-07-16T12:00:00Z',
  window: '1h0m0s',
  report: {
    currency: 'USD',
    lines: [
      { service: 'cart', signal: 'logs', ingest_monthly: 12.34, active_series_monthly: 0, total_monthly: 12.34 },
      { service: 'checkout', signal: 'metrics', ingest_monthly: 0, active_series_monthly: 4, total_monthly: 4 },
    ],
    storage: [{ class: 'STANDARD', gb: 1000, monthly: 23 }],
    total_monthly: 39.34,
  },
  lifecycle: [
    { from_class: 'STANDARD', to_class: 'GLACIER_IR', gb: 1000, current_monthly: 23, projected_monthly: 4, savings_monthly: 19 },
  ],
  trend: { currency: 'USD', total_delta: 5, lines: [{ service: 'checkout', signal: 'metrics', previous: 3, current: 4, delta: 1, percent_delta: 33.3 }] },
  notes: ['Costs are modeled from your pricing.yaml, not billed.'],
};

const mockGet = jest.fn();
jest.mock('@grafana/runtime', () => ({
  ...jest.requireActual('@grafana/runtime'),
  getBackendSrv: () => ({ get: mockGet }),
}));

describe('SpendContent', () => {
  beforeEach(() => mockGet.mockReset());

  it('shows the monthly total, attribution, lifecycle savings, and the honesty note', async () => {
    mockGet.mockResolvedValue(showback);
    render(<SpendContent />);

    expect(await screen.findByText(/39\.34/)).toBeInTheDocument();
    expect(screen.getByText('cart')).toBeInTheDocument();
    // checkout appears in both the attribution and week-over-week tables
    expect(screen.getAllByText('checkout').length).toBeGreaterThan(0);
    // lifecycle saving surfaced
    expect(screen.getByText(/19\.00/)).toBeInTheDocument();
    // modeled-not-billed honesty note is never hidden
    expect(screen.getByText(/modeled from your pricing/i)).toBeInTheDocument();
  });

  it('renders when the engine sends null slices (empty Go slices → JSON null)', async () => {
    // the real /api/cost shape with no S3 configured: storage is null, not []
    mockGet.mockResolvedValue({
      generated_at: '2026-07-16T12:00:00Z',
      window: '1h0m0s',
      report: {
        currency: 'USD',
        lines: [{ service: 'kafka', signal: 'logs', ingest_monthly: 0.53, active_series_monthly: 0, total_monthly: 0.53 }],
        storage: null,
        total_monthly: 0.53,
      },
      lifecycle: null,
      trend: null,
      notes: ['Costs are modeled from your pricing.yaml, not billed.'],
    } as unknown as Showback);
    render(<SpendContent />);
    // must not crash on storage.length of null — the total and the line render
    // (0.53 appears as the total and in the table, so findAll)
    expect((await screen.findAllByText(/0\.53/)).length).toBeGreaterThan(0);
    expect(screen.getByText('kafka')).toBeInTheDocument();
  });

  it('explains graciously when cost is not configured', async () => {
    // engine returns 404 when --pricing isn't set; the proxy relays it
    mockGet.mockRejectedValue({ status: 404, data: { message: 'cost reporting is not configured' } });
    render(<SpendContent />);
    expect(await screen.findByText(/not configured/i)).toBeInTheDocument();
  });

  it('shows the engine error instead of pretending', async () => {
    mockGet.mockRejectedValue(new Error('engine unreachable: connection refused'));
    render(<SpendContent />);
    expect(await screen.findByText(/engine unreachable/i)).toBeInTheDocument();
  });
});
