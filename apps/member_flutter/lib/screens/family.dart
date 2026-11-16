import 'package:flutter/material.dart';
import '../api/client.dart';

/// Family screen: next-of-kin nominations (versioned, shares total 100%).
class FamilyScreen extends StatefulWidget {
  final ApiClient api;
  final String memberId;
  const FamilyScreen({super.key, required this.api, required this.memberId});

  @override
  State<FamilyScreen> createState() => _FamilyScreenState();
}

class _FamilyScreenState extends State<FamilyScreen> {
  Map<String, dynamic>? nomination;
  final name = TextEditingController();
  final relationship = TextEditingController();
  final phone = TextEditingController();
  String error = '';
  String ok = '';

  @override
  void initState() {
    super.initState();
    widget.api.successionGet(widget.memberId)
        .then((r) => setState(() => nomination = r))
        .catchError((_) {});
  }

  Future<void> _save() async {
    setState(() { error = ''; ok = ''; });
    try {
      final r = await widget.api.successionNominate(widget.memberId, [{
        'name': name.text,
        'relationship': relationship.text,
        'phone': phone.text,
        'share_bps': 10000,
      }]);
      setState(() { nomination = r; ok = 'Nomination saved (v${r['version']})'; });
    } catch (e) { setState(() => error = '$e'); }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Next of kin')),
      body: ListView(padding: const EdgeInsets.all(16), children: [
        if (nomination != null)
          Card(child: Padding(padding: const EdgeInsets.all(16), child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Current nomination (v${nomination!['version']})',
                  style: const TextStyle(fontWeight: FontWeight.bold)),
              ...((nomination!['beneficiaries'] as List?) ?? []).map((b) {
                final ben = b as Map<String, dynamic>;
                return Text('${ben['name']} (${ben['relationship']}) — ${(ben['share_bps'] as int) / 100}%');
              }),
            ],
          ))),
        const SizedBox(height: 12),
        const Text('Update nomination', style: TextStyle(fontWeight: FontWeight.bold)),
        TextField(controller: name, decoration: const InputDecoration(labelText: 'Full name')),
        TextField(controller: relationship, decoration: const InputDecoration(labelText: 'Relationship')),
        TextField(controller: phone, keyboardType: TextInputType.phone,
            decoration: const InputDecoration(labelText: 'Phone')),
        const SizedBox(height: 12),
        FilledButton(onPressed: _save, child: const Text('Save (100% share)')),
        if (ok.isNotEmpty) Text(ok, style: const TextStyle(color: Colors.green)),
        if (error.isNotEmpty) Text(error, style: const TextStyle(color: Colors.red)),
      ]),
    );
  }
}
