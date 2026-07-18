import pluginJson from './plugin.json';

export const PLUGIN_BASE_URL = `/a/${pluginJson.id}`;

export enum ROUTES {
  Overview = 'overview',
  Scores = 'scores',
  ServiceGraph = 'service-graph',
  Spend = 'spend',
  Backtest = 'backtest',
}
