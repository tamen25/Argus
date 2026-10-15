import { test, expect } from './fixtures';
import { ROUTES } from '../src/constants';

// Smoke tests for every page against a mocked engine: Overview renders the
// fleet score; Scores shows a finding with its confidence badge and opens the
// remediation panel; Service graph draws the topology with its sampling
// caveat; Spend shows the modeled-not-billed note; Backtest waits out a
// report the engine is still computing; Bench shows the leaderboard.
const report = {
  generated_at: '2026-07-14T12:00:00Z',
  argus_version: 'e2e',
  spec_version: 'e6ee22274284',
  window: '1m0s',
  rule_set_complete: false,
  notes: [],
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
            confidence: 'sampled',
            stats: { observed: 1, violations: 1, ratio: 1 },
          },
        ],
      },
    ],
    rules_evaluated: ['MET-001'],
  },
};

const remediation = {
  rule_id: 'MET-001',
  service: 'cart',
  template: 'high-cardinality-attribute',
  formats: {
    'alloy.river': '// review before applying — e2e patch body',
    'collector.yaml': '# review before applying — e2e patch body',
  },
};

const serviceGraph = {
  generated_at: '2026-07-14T12:00:00Z',
  window: '1m0s',
  nodes: [
    { service: 'frontend', spec_score: 92.5, findings: 0 },
    { service: 'cart', spec_score: 76.9, findings: 1 },
  ],
  edges: [{ source: 'frontend', target: 'cart', traces: 42 }],
};

const showback = {
  generated_at: '2026-07-14T12:00:00Z',
  window: '1h0m0s',
  report: {
    currency: 'USD',
    lines: [{ service: 'cart', signal: 'logs', ingest_monthly: 12.34, active_series_monthly: 0, total_monthly: 12.34 }],
    storage: [{ class: 'STANDARD', gb: 1000, monthly: 23 }],
    total_monthly: 35.34,
  },
  lifecycle: [
    {
      from_class: 'STANDARD',
      to_class: 'GLACIER_IR',
      gb: 1000,
      current_monthly: 23,
      projected_monthly: 4,
      savings_monthly: 19,
    },
  ],
  notes: ['Costs are modeled from your pricing.yaml, not billed.'],
};

const backtest = {
  generated_at: '2026-07-18T09:00:00Z',
  from: '2026-07-18T05:10:00Z',
  to: '2026-07-18T05:45:00Z',
  step_seconds: 60,
  coverage_seconds: 2100,
  window_seconds: 2100,
  segments: 1,
  rules: [
    {
      rule: 'HighSpanErrorRatio',
      detections: [{ incident_id: 'inc-1', ttd_seconds: 420 }],
      missed: [],
      unverifiable: [],
      false_positives: [],
      coverage_seconds: 2100,
      pages_per_week: 3,
      flappiness: 1,
    },
  ],
  caveats: [],
};

const bench = {
  reports: 1,
  conditions: ['degraded'],
  leaderboard: {
    boards: [
      {
        condition: 'degraded',
        scenarios: ['oomkill-checkout'],
        rows: [
          {
            agent: 'agent-x',
            cells: [
              {
                scenario: 'oomkill-checkout',
                attempts: 1,
                diagnoses: 1,
                budget_exhausted: 0,
                answer_rate: 1,
                mean_score: 1,
                stddev_score: 0,
              },
            ],
            scenarios_run: 1,
            scenarios_answered: 1,
            mean_score: 1,
            attempts: 1,
            diagnoses: 1,
            answer_rate: 1,
          },
        ],
      },
    ],
    caveats: ['No significance test is applied. Compare with the spread and the number of runs.'],
  },
};

test.beforeEach(async ({ page }) => {
  await page.route('**/api/plugins/*/resources/scores', (route) => route.fulfill({ json: report }));
  await page.route('**/api/plugins/*/resources/remediation*', (route) => route.fulfill({ json: remediation }));
  await page.route('**/api/plugins/*/resources/servicegraph', (route) => route.fulfill({ json: serviceGraph }));
  await page.route('**/api/plugins/*/resources/cost', (route) => route.fulfill({ json: showback }));
});

test.describe('argus app', () => {
  test('overview shows the fleet score and disclosure', async ({ gotoPage, page }) => {
    await gotoPage(`/${ROUTES.Overview}`);
    await expect(page.getByText('84.7')).toBeVisible();
    await expect(page.getByText(/does not yet implement the full rule set/i)).toBeVisible();
    await expect(page.getByText('cart')).toBeVisible();
  });

  test('scores drills into a finding and opens the remediation panel', async ({ gotoPage, page }) => {
    await gotoPage(`/${ROUTES.Scores}`);
    await expect(page.getByText('bounded metric attribute cardinality')).toBeVisible();
    // exact + scoped: the footnote paragraph also contains the word "sampled"
    await expect(page.getByTestId('data-testid scores-finding').getByText('sampled', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: /view remediation/i }).click();
    await expect(page.getByText(/e2e patch body/).first()).toBeVisible();
    await expect(page.getByRole('button', { name: /copy/i }).first()).toBeVisible();
  });

  test('service graph draws the topology and keeps the sampling caveat visible', async ({ gotoPage, page }) => {
    await gotoPage(`/${ROUTES.ServiceGraph}`);
    await expect(page.getByText(/absence here is not evidence of absence/i)).toBeVisible();
    await expect(page.getByText('frontend').first()).toBeVisible();
  });

  test('spend shows the monthly total, lifecycle savings, and the modeled-not-billed note', async ({
    gotoPage,
    page,
  }) => {
    await gotoPage(`/${ROUTES.Spend}`);
    await expect(page.getByText(/35\.34/)).toBeVisible();
    await expect(page.getByText(/STANDARD → GLACIER_IR/)).toBeVisible();
    await expect(page.getByText(/modeled from your pricing/i)).toBeVisible();
  });

  test('backtest waits while the engine computes, then shows the report and its caveats', async ({
    gotoPage,
    page,
  }) => {
    // The first request finds the replay still running (503); the page says
    // so and asks again, and the second request gets the report.
    let calls = 0;
    await page.route('**/api/plugins/*/resources/backtest', (route) => {
      calls++;
      return calls === 1
        ? route.fulfill({ status: 503, body: 'the backtest is still being computed; try again shortly' })
        : route.fulfill({ json: backtest });
    });
    await page.clock.install();
    await gotoPage(`/${ROUTES.Backtest}`);
    await expect(page.getByText(/still computing it/i)).toBeVisible();
    await page.clock.fastForward(10_000);
    await expect(page.getByText('HighSpanErrorRatio')).toBeVisible();
    await expect(page.getByText(/Replay steps instant queries through history/i)).toBeVisible();
  });

  test('bench shows the leaderboard with its caveats', async ({ gotoPage, page }) => {
    await page.route('**/api/plugins/*/resources/bench*', (route) => route.fulfill({ json: bench }));
    await gotoPage(`/${ROUTES.Bench}`);
    await expect(page.getByText('Leaderboard · degraded')).toBeVisible();
    await expect(page.getByText(/no significance test/i)).toBeVisible();
  });
});
