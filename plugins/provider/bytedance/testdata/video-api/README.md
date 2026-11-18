# ModelArk task API parameter fixture

This normalized factual fixture isolates Seedance 2.5 from the other model columns
in the public Singapore task API document. It is not a complete or verbatim source
copy. The provenance file retains the exact raw/normalized document hashes and
retrieval metadata. The official page advertises its public Markdown endpoint;
the older documentation alias's JavaScript shell is not substituted as evidence.

`seedance-2-5-series-parameters.txt` keeps the per-model layout retrieved on
2026-11-15, where Dreamina Seedance 2.5 and 2.5 flash share series entries but
publish their own resolutions and reference counts. Its lines are copied in source
order with unrelated prose omitted; the last line shortens the request example's
prompt. `seedance-2-5-series-provenance.json` records the hashes. The base 2.5
resolution line and 4k table rows were refreshed on 2026-11-17, after BytePlus
added 4k for base 2.5 on 2026-11-16 (same document hash as the GW-389 capture);
the layout is unchanged. The October fixture remains for the earlier layout.

The existing exact adapter ID-to-family binding is explicit. A future model,
snapshot or task mode requires a separate review; no prices or execution controls
are inferred from this fixture. Exact downloaded source replay is documented in
the [Alibaba fixture notes](../../../alibaba/testdata/video-api/README.md).
