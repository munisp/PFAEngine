import 'package:flutter/material.dart';
import 'theme/tenant_theme.dart';
import 'screens/onboarding.dart';
import 'screens/dashboard.dart';

/// PFAEngine member app entry point.
/// Tenant branding is injected at build/runtime (PLT-14 white-label).
void main() {
  const tenantId = String.fromEnvironment('TENANT_ID', defaultValue: 'demo-pfa');
  const apiBase = String.fromEnvironment('API_BASE', defaultValue: 'http://localhost:8080');
  runApp(PFAApp(tenantId: tenantId, apiBase: apiBase, tenantTheme: TenantTheme.demo()));
}

class PFAApp extends StatelessWidget {
  final String tenantId;
  final String apiBase;
  final TenantTheme tenantTheme;

  const PFAApp({super.key, required this.tenantId, required this.apiBase, required this.tenantTheme});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: tenantTheme.appName,
      theme: tenantTheme.materialTheme(),
      home: OnboardingScreen(tenantId: tenantId, apiBase: apiBase),
      routes: {
        '/dashboard': (ctx) => DashboardScreen(tenantId: tenantId, apiBase: apiBase),
      },
    );
  }
}
