import React from 'react';
import { render, screen } from '@testing-library/react';
import { ServiceGraphContent } from './ServiceGraphContent';
import { ServiceGraph } from '../../api/types';

const graph: ServiceGraph = {
  generated_at: '2026-07-15T12:00:00Z',
  window: '1h0m0s',
  nodes: [
    { service: 'frontend', spec_score: 92.5, findings: 1 },
    { service: 'checkout', spec_score: 100, findings: 0 },
  ],
  edges: [{ source: 'frontend', target: 'checkout', traces: 42 }],
};

const mockGet = jest.fn();
jest.mock('@grafana/runtime', () => ({
  ...jest.requireActual('@grafana/runtime'),
  getBackendSrv: () => ({ get: mockGet }),
  // the real nodeGraph panel needs Grafana's panel plugin registry — assert
  // what we hand it instead of rendering it
  PanelRenderer: (props: { pluginId: string; data: { series: unknown[] } }) => (
    <div data-testid="node-graph-panel" data-plugin={props.pluginId} data-series={props.data.series.length} />
  ),
}));

describe('ServiceGraphContent', () => {
  beforeEach(() => {
    mockGet.mockReset();
  });

  it('renders the node graph from engine edges and disclosures', async () => {
    mockGet.mockResolvedValue(graph);
    render(<ServiceGraphContent />);

    const panel = await screen.findByTestId('node-graph-panel');
    expect(panel).toHaveAttribute('data-plugin', 'nodeGraph');
    expect(panel).toHaveAttribute('data-series', '2'); // nodes + edges frames
    // sampling honesty: an absent edge may just be unsampled
    expect(screen.getByText(/sampled mirror/i)).toBeInTheDocument();
  });

  it('says so when no cross-service traces were seen yet', async () => {
    mockGet.mockResolvedValue({ ...graph, nodes: [], edges: [] });
    render(<ServiceGraphContent />);
    expect(await screen.findByText(/no cross-service traces observed/i)).toBeInTheDocument();
  });

  it('shows the engine error instead of pretending', async () => {
    mockGet.mockRejectedValue(new Error('engine unreachable: connection refused'));
    render(<ServiceGraphContent />);
    expect(await screen.findByText(/engine unreachable/i)).toBeInTheDocument();
  });
});
