package jobs

// PurgeBatchSize exposes purgeBatchSize to the external jobs_test package, so the batching test
// seeds just past it rather than hard-coding a number that could drift.
const PurgeBatchSize = purgeBatchSize
