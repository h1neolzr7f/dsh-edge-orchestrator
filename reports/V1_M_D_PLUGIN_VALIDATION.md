# M-D Cordis tools validation

All eight fleet tools implemented in integrations/dsh-cordis-plugin, without runtime dependencies. Actual pinned DSH ToolRegistry.register contract inspected from @deepseek-ai/dsh-tools0.2.2-rc.1 and upstream dsh-go f5fa9256db4c82d601078b8064056b93dc3560ec; pinned SDK assertObjectJsonSchema/assertSupportedJsonSchema executed against every exported tool definition.

RED: missing client module. GREEN: all eight tools call a real compiled Go Coordinator and two paired agent child processes over certificate-verified HTTPS. Supplemental RED tests found chunk-size loss after the first4MiB PATCH and an unsupported minimum schema keyword in the host SDK; both fixed and GREEN. Client abort errors consistently reject promises.

2026-11-16 results:
- scripts/check.sh all eight Go modules test/vet + native Node24 integration PASS,2.161s test /5.291s runner.
- Native Node18 integration PASS,2.202s test /5.325s runner (actual npm Node18 binary).
- Pinned SDK schema validator PASS for all eight definitions.
- TLS/custom CA required, untrusted certificate/HTTP rejected; no redirects or TLS verification disabling.
- Actual process logs, Git inspection, approval, timeout/AbortSignal, idempotent cancellation, artifact hash/size,4MiB+123-byte upload and repeated completion PASS.
- Download traversal/symlink rejection PASS. Artifact names never select local filenames; workspaceRoot must be private to its owner.

API: FleetClient methods are documented in package README. apply(ctx,config), createTools(config), toolDefinitions and validatePlan exported. Cordis bundle patch uses real insert id/name contract. DSH retains its model routing/chat loop. This is a tested embeddable plugin; no modified upstream APK build is claimed.
