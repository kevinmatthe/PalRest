# Progressive map replay

Goal: replace the fixed latest-500 history with bounded, cursor-driven loading, without confusing unloaded evidence with real observation gaps.

- [x] Backend adds full public range bounds independent of LIMIT, in the same snapshot transaction as counts/data; private samples do not affect bounds.
- [x] Frontend bootstraps metadata, loads ten-minute cursor chunks with continuity overlap, prefetches next chunk, and evicts distant chunks. Split dense ranges instead of silently truncating; report unsplittable limits explicitly.
- [x] Playback clock stops at buffered coverage while keeping play intent; resumes without skipping after arrival. Explicit seek pauses playback and triggers target loading. Metadata/cache changes do not reset cursor.
- [x] Map messages distinguish buffering, error, and true empty intervals; slider spans full range. Journey statistics use only the buffered interval.
- [x] Tests cover metadata privacy/bounds, >500 rows, overlap/deduplication, cancel/seek, bounded caching, buffered clock behavior, and the component flow. Verify frontend build and Go suites.

Implementation notes: timeline bounds describe public evidence across the query; buffer bounds describe fetched coverage, including empty portions. Progress timestamps can therefore be replayed beyond the last location without a permanent buffer stall. Network errors, timeouts, and excessive density retain explicit error/buffering states.
