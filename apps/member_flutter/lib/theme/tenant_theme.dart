import 'package:flutter/material.dart';

/// Per-tenant white-label theming (PLT-14) — maps tenant branding config
/// (colors, logo, app name) onto Flutter widget themes.
class TenantTheme {
  final String appName;
  final Color primary;
  final Color secondary;
  final String? logoUrl;

  const TenantTheme({required this.appName, required this.primary, required this.secondary, this.logoUrl});

  factory TenantTheme.demo() => const TenantTheme(
        appName: 'Demo PFA',
        primary: Color(0xFF0B5FFF),
        secondary: Color(0xFF00B386),
      );

  factory TenantTheme.fromJson(Map<String, dynamic> json) => TenantTheme(
        appName: json['app_name'] ?? 'PFA',
        primary: Color(int.parse((json['primary'] ?? '#0B5FFF').substring(1), radix: 16) + 0xFF000000),
        secondary: Color(int.parse((json['secondary'] ?? '#00B386').substring(1), radix: 16) + 0xFF000000),
        logoUrl: json['logo_url'],
      );

  ThemeData materialTheme() => ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: primary, secondary: secondary),
        useMaterial3: true,
      );
}
