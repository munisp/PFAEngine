import 'package:flutter/material.dart';
import 'package:qr_flutter/qr_flutter.dart';
import '../api/client.dart';

/// Member dashboard: balance, transactions, QR-verifiable statement (FR-501),
/// I-Am-Alive status (FR-108).
class DashboardScreen extends StatelessWidget {
  final String tenantId;
  final String apiBase;
  const DashboardScreen({super.key, required this.tenantId, required this.apiBase});

  @override
  Widget build(BuildContext context) {
    final api = ApiClient(baseUrl: apiBase, tenantId: tenantId);
    return Scaffold(
      appBar: AppBar(title: const Text('My Pension')),
      body: FutureBuilder<Map<String, dynamic>>(
        future: api.statement('demo-rsa', '2026-11'),
        builder: (ctx, snap) {
          if (!snap.hasData) return const Center(child: CircularProgressIndicator());
          final s = snap.data!;
          return ListView(padding: const EdgeInsets.all(16), children: [
            Card(child: ListTile(
              title: const Text('Closing balance'),
              subtitle: Text('Period: ${s['period']}'),
              trailing: Text('NGN ${s['closing_kobo'] ?? 0}', style: const TextStyle(fontSize: 20, fontWeight: FontWeight.bold)),
            )),
            const SizedBox(height: 12),
            Center(child: QrImageView(data: 'verify:${s['verify_code']}', size: 160)),
            const Center(child: Text('Statement verification QR')),
            const SizedBox(height: 12),
            FilledButton.icon(
              icon: const Icon(Icons.verified_user),
              label: const Text('I Am Alive — verify now'),
              onPressed: () => api.iAmAlive('demo-person', 'clean-selfie'),
            ),
          ]);
        },
      ),
    );
  }
}
