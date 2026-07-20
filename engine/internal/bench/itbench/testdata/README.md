# ITBench test fixtures

`itbench-1.json` and `itbench-102.json` are verbatim copies of scenario index
files from [itbench-hub/ITBench](https://github.com/itbench-hub/ITBench)
(`scenarios/sre/library/indexes/scenarios/{1,102}.json`), Apache-2.0.

They are vendored as fixtures so the importer is tested against the **real**
upstream shape rather than a fixture we invented. Scenario 1 is the useful hard
case: its injected object is a `ConfigMap` while its `waitFor` block restarts two
`Deployment`s — only the injected object is ground truth.
