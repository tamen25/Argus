import React from 'react';
import { act, render, screen } from '@testing-library/react';
import { BacktestContent } from './BacktestContent';
import { BacktestReport } from '../../api/types';

const report: BacktestReport = {
  generated_at: '2026-07-18T09:00:00Z',
  from: '2026-07-18T05:10:00Z',
  to: '2026-07-18T05:45:00Z',
  step_seconds: 60,
  coverage_seconds: 1920,
  window_seconds: 2100,
  segments: 1,
  rules: [
    {
      rule: 'HighSpanErrorRatio',
      detections: [{ incident_id: '2026-07-18-adfailure-baseline-2', ttd_seconds: 420 }],
      missed: ['2026-07-12-adfailure-toggle-test'],
      unverifiable: ['2026-07-16-adfailure-spike-baseline'],
      false_positives: [],
      coverage_seconds: 1920,
      pages_per_week: 31.5,
      flappiness: 1,
    },
  ],
  caveats: ['telemetry covers 32m0s of the 35m0s window — verdicts apply to covered segments only'],
};

const mockGet = jest.fn();
jest.mock('@grafana/runtime', () => ({
  ...jest.requireActual('@grafana/runtime'),
  getBackendSrv: () => ({ get: mockGet }),
}));

describe('BacktestContent', () => {
  beforeEach(() => mockGet.mockReset());

  it('shows coverage, the detection with TTD, missed/unverifiable, and the fidelity caveat', async () => {
    mockGet.mockResolvedValue(report);
    render(<BacktestContent />);

    // coverage header (1920s → 32m, 2100s → 35m)
    expect(await screen.findByText(/32m covered/)).toBeInTheDocument();
    expect(screen.getByText('HighSpanErrorRatio')).toBeInTheDocument();
    // TTD 420s → 7m
    expect(screen.getByText('7m')).toBeInTheDocument();
    expect(screen.getByText(/missed: 2026-07-12-adfailure-toggle-test/)).toBeInTheDocument();
    expect(screen.getByText(/unverifiable.*2026-07-16-adfailure-spike-baseline/)).toBeInTheDocument();
    // fidelity caveat is never hidden
    expect(screen.getByText(/Replay fidelity/i)).toBeInTheDocument();
    expect(screen.getByText(/verdicts apply to the covered segments only/i)).toBeInTheDocument();
  });

  it('says so when no rule was replayed', async () => {
    mockGet.mockResolvedValue({
      generated_at: '2026-07-18T09:00:00Z',
      from: '2026-07-18T05:10:00Z',
      to: '2026-07-18T05:45:00Z',
      step_seconds: 60,
      coverage_seconds: 0,
      window_seconds: 2100,
      segments: 0,
      rules: [],
      caveats: [],
    } satisfies BacktestReport);
    render(<BacktestContent />);
    expect(await screen.findByText(/No alerting rules were replayed/i)).toBeInTheDocument();
  });

  it('explains graciously when backtest is not configured', async () => {
    mockGet.mockRejectedValue({ status: 404, data: { message: 'backtest is not configured' } });
    render(<BacktestContent />);
    expect(await screen.findByText(/not configured/i)).toBeInTheDocument();
  });

  it('shows the engine error instead of pretending', async () => {
    mockGet.mockRejectedValue(new Error('engine unreachable: connection refused'));
    render(<BacktestContent />);
    expect(await screen.findByText(/engine unreachable/i)).toBeInTheDocument();
  });

  // R13: a replay longer than the engine's response wait is answered 503.
  // The page says it is computing and asks again, rather than failing.
  it('waits while the engine is still computing, then shows the report', async () => {
    jest.useFakeTimers();
    try {
      mockGet.mockRejectedValueOnce({ status: 503 }).mockResolvedValue(report);
      render(<BacktestContent />);
      expect(await screen.findByText(/still computing it/)).toBeInTheDocument();
      await act(async () => {
        jest.advanceTimersByTime(10_000);
      });
      expect(await screen.findByText('HighSpanErrorRatio')).toBeInTheDocument();
      expect(mockGet).toHaveBeenCalledTimes(2);
    } finally {
      jest.useRealTimers();
    }
  });

  // R33: a refresh that fails keeps the last report on screen and says the
  // refresh failed, so stale data never passes for live.
  it('keeps the report when a refresh fails, and says so', async () => {
    jest.useFakeTimers();
    try {
      mockGet.mockResolvedValueOnce(report).mockRejectedValue({ status: 502, message: 'engine down' });
      render(<BacktestContent />);
      expect(await screen.findByText('HighSpanErrorRatio')).toBeInTheDocument();
      await act(async () => {
        jest.advanceTimersByTime(60_000);
      });
      expect(await screen.findByText(/Refresh failed: engine down/)).toBeInTheDocument();
      expect(screen.getByText('HighSpanErrorRatio')).toBeInTheDocument();
    } finally {
      jest.useRealTimers();
    }
  });
});
