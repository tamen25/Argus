import { test, expect } from './fixtures';
import { ROUTES } from '../src/constants';

// Smoke for the Phase 1 pages: Overview renders the fleet score from the
// (mocked) engine; Scores shows a finding with its confidence badge and
// opens the remediation panel; Service graph draws trace-derived topology
// with its sampling caveat.
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

test.beforeEach(async ({ page }) => {
  await page.route('**/api/plugins/*/resources/scores', (route) =>
    route.fulfill({ json: report })
  );
  await page.route('**/api/plugins/*/resources/remediation*', (route) =>
    route.fulfill({ json: remediation })
  );
  await page.route('**/api/plugins/*/resources/servicegraph', (route) =>
    route.fulfill({ json: serviceGraph })
  );
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
    await expect(
      page.getByTestId('data-testid scores-finding').getByText('sampled', { exact: true })
    ).toBeVisible();
    await page.getByRole('button', { name: /view remediation/i }).click();
    await expect(page.getByText(/e2e patch body/).first()).toBeVisible();
    await expect(page.getByRole('button', { name: /copy/i }).first()).toBeVisible();
  });

  test('service graph draws the topology and keeps the sampling caveat visible', async ({ gotoPage, page }) => {
    await gotoPage(`/${ROUTES.ServiceGraph}`);
    await expect(page.getByText(/absence here is not evidence of absence/i)).toBeVisible();
    await expect(page.getByText('frontend').first()).toBeVisible();
  });
});
