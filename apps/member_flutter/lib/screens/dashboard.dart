import 'package:flutter/material.dart';
import 'package:qr_flutter/qr_flutter.dart';
import '../api/client.dart';
import 'grow.dart';
import 'funds.dart';
import 'claims.dart';
import 'family.dart';

/// Member dashboard: balance, transactions, QR-verifiable statement (FR-501),
/// I-Am-Alive status (FR-108).
Widget _navChip(BuildContext context, IconData icon, String label, Widget dest) {
  return ActionChip(
    avatar: Icon(icon, size: 18),
    label: Text(label),
    onPressed: () => Navigator.of(context).push(MaterialPageRoute(builder: (_) => dest)),
  );
}

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
            const SizedBox(height: 16),
            const Text('Innovations', style: TextStyle(fontWeight: FontWeight.bold)),
            const SizedBox(height: 8),
            Wrap(spacing: 8, runSpacing: 8, children: [
              _navChip(context, Icons.trending_up, 'Grow',
                  GrowScreen(api: api, memberId: 'demo-rsa')),
              _navChip(context, Icons.account_balance, 'Funds',
                  FundsScreen(api: api, memberId: 'demo-rsa')),
              _navChip(context, Icons.assignment_turned_in, 'Claims',
                  ClaimsScreen(api: api, rsaId: 'demo-rsa')),
              _navChip(context, Icons.family_restroom, 'Family',
                  FamilyScreen(api: api, memberId: 'demo-rsa')),
            ]),
          ]);
        },
      ),
    );
  }
}
