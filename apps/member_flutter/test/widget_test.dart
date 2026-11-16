import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:pfaengine_member/main.dart';
import 'package:pfaengine_member/theme/tenant_theme.dart';

void main() {
  testWidgets('app boots into onboarding with tenant branding', (tester) async {
    await tester.pumpWidget(PFAApp(
      tenantId: 'demo-pfa',
      apiBase: 'http://localhost:8080',
      tenantTheme: TenantTheme.demo(),
    ));
    await tester.pump();
    // Onboarding wizard is the entry point (FR-101..107).
    expect(find.byType(Stepper), findsOneWidget);
  });

  testWidgets('dashboard renders loading state before API data arrives', (tester) async {
    await tester.pumpWidget(PFAApp(
      tenantId: 'demo-pfa',
      apiBase: 'http://localhost:8080',
      tenantTheme: TenantTheme.demo(),
    ));
    await tester.pump();
    final ctx = tester.element(find.byType(Stepper));
    Navigator.of(ctx).pushNamed('/dashboard');
    await tester.pump();
    await tester.pump(const Duration(seconds: 1)); // route transition
    // Statement fetch is in flight (no backend in tests) — spinner shows.
    expect(find.text('My Pension'), findsOneWidget);
    expect(find.byType(CircularProgressIndicator), findsOneWidget);
  });

  test('tenant theme parses white-label JSON', () {
    final t = TenantTheme.fromJson(const {
      'app_name': 'Acme Pensions',
      'primary': '#112233',
      'secondary': '#445566',
    });
    expect(t.appName, 'Acme Pensions');
    expect(t.primary.value, 0xFF112233);
    expect(t.secondary.value, 0xFF445566);
  });

  test('tenant theme falls back to defaults on missing keys', () {
    final t = TenantTheme.fromJson(const {});
    expect(t.appName, 'PFA');
    expect(t.primary.value, 0xFF0B5FFF);
  });
}
