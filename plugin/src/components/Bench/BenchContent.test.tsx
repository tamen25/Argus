import React from 'react';
import { render, screen } from '@testing-library/react';
import { BenchContent } from './BenchContent';
import { BenchComparison, BenchLeaderboard, BenchResponse } from '../../api/types';

const cell = (scenario: string, mean: number, sd: number, diagnoses: number, attempts: number) => ({
  scenario,
  attempts,
  diagnoses,
  budget_exhausted: attempts - diagnoses,
  answer_rate: attempts ? diagnoses / attempts : 0,
  mean_score: mean,
  stddev_score: sd,
});

const leaderboard: BenchLeaderboard = {
  boards: [
    {
      condition: 'degraded',
      scenarios: ['oomkill-checkout', 'redis-latency-cart'],
      rows: [
        {
          agent: 'model-a',
          cells: [cell('oomkill-checkout', 0.25, 0.25, 2, 2), cell('redis-latency-cart', 0, 0, 0, 2)],
          scenarios_run: 2,
          scenarios_answered: 1,
          mean_score: 0.25,
          attempts: 4,
          diagnoses: 2,
          answer_rate: 0.5,
        },
      ],
    },
    {
      condition: 'remediated',
      scenarios: ['oomkill-checkout', 'redis-latency-cart'],
      rows: [
        {
          agent: 'model-a',
          cells: [cell('oomkill-checkout', 1, 0, 2, 2), cell('redis-latency-cart', 0.5, 0, 2, 2)],
          scenarios_run: 2,
          scenarios_answered: 2,
          mean_score: 0.75,
          attempts: 4,
          diagnoses: 4,
          answer_rate: 1,
        },
      ],
    },
  ],
  caveats: ['The telemetry condition is a label the operator attached to each run.'],
};

const comparison: BenchComparison = {
  baseline: 'degraded',
  treatment: 'remediated',
  agents: [
    {
      agent: 'model-a',
      scenarios: [
        {
          scenario: 'oomkill-checkout',
          baseline: cell('oomkill-checkout', 0.25, 0.25, 2, 2),
          treatment: cell('oomkill-checkout', 1, 0, 2, 2),
          delta: 0.75,
        },
        {
          scenario: 'redis-latency-cart',
          baseline: cell('redis-latency-cart', 0, 0, 0, 2),
          treatment: cell('redis-latency-cart', 0.5, 0, 2, 2),
        },
      ],
      paired: 1,
      baseline_mean: 0.25,
      treatment_mean: 1,
      delta: 0.75,
      baseline_attempts: 4,
      baseline_diagnoses: 2,
      baseline_answer_rate: 0.5,
      treatment_attempts: 4,
      treatment_diagnoses: 4,
      treatment_answer_rate: 1,
      excluded: ['redis-latency-cart: no diagnosis under `degraded` (2 attempts)'],
    },
  ],
  caveats: ['No significance test is applied. Compare Δ with the ± spread and the number of runs.'],
};

const response: BenchResponse = { reports: 4, conditions: ['degraded', 'remediated'], leaderboard };

const mockGet = jest.fn();
jest.mock('@grafana/runtime', () => ({
  ...jest.requireActual('@grafana/runtime'),
  getBackendSrv: () => ({ get: mockGet }),
}));

describe('BenchContent', () => {
  beforeEach(() => mockGet.mockReset());

  it('opens on the degraded → remediated comparison, with the leaderboards and caveats', async () => {
    mockGet.mockImplementation((_url: string, params?: { compare?: string }) =>
      Promise.resolve(params?.compare ? { ...response, comparison } : response)
    );
    render(<BenchContent />);

    // The comparison is requested for the flagship's two conditions.
    expect(await screen.findByText('degraded → remediated')).toBeInTheDocument();
    expect(mockGet).toHaveBeenCalledWith(expect.stringMatching(/\/bench$/), { compare: 'degraded,remediated' });
    expect(screen.getByText('1 of 2')).toBeInTheDocument();
    expect(screen.getAllByText('+0.75').length).toBeGreaterThan(0);
    // A scenario unanswered on one side is listed, not counted as zero.
    expect(screen.getByText(/excluded: redis-latency-cart: no diagnosis/)).toBeInTheDocument();
    // One leaderboard per condition, with honest cells.
    expect(screen.getByText('Leaderboard · degraded')).toBeInTheDocument();
    expect(screen.getByText('Leaderboard · remediated')).toBeInTheDocument();
    expect(screen.getAllByText('no diagnosis (0/2)').length).toBeGreaterThan(0);
    // The comparison's caveats are shown, never hidden.
    expect(screen.getByText(/No significance test is applied/)).toBeInTheDocument();
  });

  it('shows the leaderboard alone when the conditions are not the flagship pair', async () => {
    mockGet.mockResolvedValue({
      ...response,
      conditions: ['degraded'],
      leaderboard: { ...leaderboard, boards: [leaderboard.boards[0]] },
    });
    render(<BenchContent />);
    expect(await screen.findByText('Leaderboard · degraded')).toBeInTheDocument();
    expect(screen.queryByText(/→/)).not.toBeInTheDocument();
    expect(mockGet).toHaveBeenCalledTimes(1);
    expect(screen.getByText(/label the operator attached/)).toBeInTheDocument();
  });

  it('says there are no runs yet rather than showing an empty table', async () => {
    mockGet.mockResolvedValue({ reports: 0, conditions: [] });
    render(<BenchContent />);
    expect(await screen.findByText(/No bench runs yet/)).toBeInTheDocument();
  });

  it('explains how to configure it when the engine has no reports directory', async () => {
    mockGet.mockRejectedValue({ status: 404, data: { message: 'bench is not configured' } });
    render(<BenchContent />);
    expect(await screen.findByText(/Bench is not configured/)).toBeInTheDocument();
  });

  it('shows the engine error instead of pretending', async () => {
    mockGet.mockRejectedValue({ status: 500, data: { message: 'x.json: not a bench run report' } });
    render(<BenchContent />);
    expect(await screen.findByText(/not a bench run report/)).toBeInTheDocument();
  });
});
