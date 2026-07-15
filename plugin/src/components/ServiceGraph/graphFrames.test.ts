import { buildGraphFrames } from './graphFrames';
import { ServiceGraph } from '../../api/types';

const graph: ServiceGraph = {
  generated_at: '2026-07-15T12:00:00Z',
  window: '1h0m0s',
  nodes: [
    { service: 'frontend', spec_score: 92.5, findings: 1 },
    { service: 'checkout', spec_score: 100, findings: 0 },
    { service: 'mystery', findings: 0 },
  ],
  edges: [{ source: 'frontend', target: 'checkout', traces: 42 }],
};

const field = (frame: { fields: Array<{ name: string; values: unknown }> }, name: string) => {
  const f = frame.fields.find((x) => x.name === name);
  if (!f) {
    throw new Error(`no field ${name}`);
  }
  return f;
};

describe('buildGraphFrames', () => {
  it('builds the nodeGraph panel contract: ids, stats, score arcs', () => {
    const { nodes, edges } = buildGraphFrames(graph);

    expect(nodes.name).toBe('nodes');
    expect(field(nodes, 'id').values).toEqual(['frontend', 'checkout', 'mystery']);
    expect(field(nodes, 'title').values).toEqual(['frontend', 'checkout', 'mystery']);
    expect(field(nodes, 'mainstat').values).toEqual(['92.5', '100.0', 'unscored']);
    expect(field(nodes, 'secondarystat').values).toEqual(['1 finding', '0 findings', '0 findings']);
    // arc = score fraction (green) vs gap (red); an unscored node draws no arc
    expect(field(nodes, 'arc__score').values).toEqual([0.925, 1, 0]);
    const gaps = field(nodes, 'arc__gap').values as number[];
    expect(gaps[0]).toBeCloseTo(0.075);
    expect(gaps[1]).toBe(0);
    expect(gaps[2]).toBe(0);

    expect(edges.name).toBe('edges');
    expect(field(edges, 'id').values).toEqual(['frontend→checkout']);
    expect(field(edges, 'source').values).toEqual(['frontend']);
    expect(field(edges, 'target').values).toEqual(['checkout']);
    expect(field(edges, 'mainstat').values).toEqual(['42 traces']);
  });

  it('handles an empty graph without inventing rows', () => {
    const { nodes, edges } = buildGraphFrames({ generated_at: '', window: '', nodes: [], edges: [] });
    expect(nodes.length).toBe(0);
    expect(edges.length).toBe(0);
  });
});
