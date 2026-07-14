import React from 'react';
import { AppRootProps } from '@grafana/data';
import { EmbeddedScene, SceneApp, SceneAppPage, SceneFlexItem, SceneFlexLayout, SceneReactObject, useSceneApp } from '@grafana/scenes';
import { PLUGIN_BASE_URL, ROUTES } from '../../constants';
import { OverviewContent } from '../Overview/OverviewContent';
import { ScoresContent } from '../Scores/ScoresContent';

// Scenes app shell (master plan §8: Scenes for all data-bound pages);
// page content stays plain @grafana/ui React rendered via SceneReactObject.
function reactScene(component: React.ComponentType) {
  return () =>
    new EmbeddedScene({
      body: new SceneFlexLayout({
        children: [
          new SceneFlexItem({
            body: new SceneReactObject({ component }),
          }),
        ],
      }),
    });
}

function getSceneApp() {
  return new SceneApp({
    pages: [
      new SceneAppPage({
        title: 'Overview',
        subTitle: 'Fleet instrumentation health — Instrumentation Score over the live telemetry mirror',
        url: `${PLUGIN_BASE_URL}/${ROUTES.Overview}`,
        routePath: `${ROUTES.Overview}/*`,
        getScene: reactScene(OverviewContent),
      }),
      new SceneAppPage({
        title: 'Scores',
        subTitle: 'Per-service findings — evidence, confidence, and remediation patches',
        url: `${PLUGIN_BASE_URL}/${ROUTES.Scores}`,
        routePath: `${ROUTES.Scores}/*`,
        getScene: reactScene(ScoresContent),
      }),
    ],
  });
}

function App(_props: AppRootProps) {
  const scene = useSceneApp(getSceneApp);
  return <scene.Component model={scene} />;
}

export default App;
