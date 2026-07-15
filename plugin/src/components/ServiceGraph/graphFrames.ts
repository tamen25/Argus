import { DataFrame, FieldColorModeId, FieldType } from '@grafana/data';
import { ServiceGraph } from '../../api/types';

// buildGraphFrames shapes the engine's /servicegraph response into the two
// data frames the core nodeGraph panel expects (fields per the Grafana node
// graph data API: id/title/mainstat/secondarystat/arc__* on nodes,
// id/source/target/mainstat on edges). Pure and deterministic — the engine
// already sorted nodes and edges.
export function buildGraphFrames(graph: ServiceGraph): { nodes: DataFrame; edges: DataFrame } {
  const meta = { preferredVisualisationType: 'nodeGraph' as const };
  const nodes = graph.nodes ?? [];
  const edges = graph.edges ?? [];

  const nodesFrame: DataFrame = {
    name: 'nodes',
    refId: 'nodes',
    meta,
    length: nodes.length,
    fields: [
      { name: 'id', type: FieldType.string, values: nodes.map((n) => n.service), config: {} },
      { name: 'title', type: FieldType.string, values: nodes.map((n) => n.service), config: {} },
      {
        name: 'mainstat',
        type: FieldType.string,
        values: nodes.map((n) => (n.spec_score == null ? 'unscored' : n.spec_score.toFixed(1))),
        config: { displayName: 'spec score' },
      },
      {
        name: 'secondarystat',
        type: FieldType.string,
        values: nodes.map((n) => `${n.findings} finding${n.findings === 1 ? '' : 's'}`),
        config: {},
      },
      // score fraction as a green arc, the gap in red; unscored nodes draw
      // no arc rather than faking a perfect or empty score
      {
        name: 'arc__score',
        type: FieldType.number,
        values: nodes.map((n) => (n.spec_score == null ? 0 : n.spec_score / 100)),
        config: { color: { mode: FieldColorModeId.Fixed, fixedColor: 'green' } },
      },
      {
        name: 'arc__gap',
        type: FieldType.number,
        values: nodes.map((n) => (n.spec_score == null ? 0 : 1 - n.spec_score / 100)),
        config: { color: { mode: FieldColorModeId.Fixed, fixedColor: 'red' } },
      },
    ],
  };

  const edgesFrame: DataFrame = {
    name: 'edges',
    refId: 'edges',
    meta,
    length: edges.length,
    fields: [
      { name: 'id', type: FieldType.string, values: edges.map((e) => `${e.source}→${e.target}`), config: {} },
      { name: 'source', type: FieldType.string, values: edges.map((e) => e.source), config: {} },
      { name: 'target', type: FieldType.string, values: edges.map((e) => e.target), config: {} },
      {
        name: 'mainstat',
        type: FieldType.string,
        values: edges.map((e) => `${e.traces} trace${e.traces === 1 ? '' : 's'}`),
        config: {},
      },
    ],
  };

  return { nodes: nodesFrame, edges: edgesFrame };
}
