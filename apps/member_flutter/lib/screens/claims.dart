import 'package:flutter/material.dart';
import '../api/client.dart';

/// Claims screen: fast-track retirement claim checklist (straight-through
/// processing at 100/100).
class ClaimsScreen extends StatefulWidget {
  final ApiClient api;
  final String rsaId;
  const ClaimsScreen({super.key, required this.api, required this.rsaId});

  @override
  State<ClaimsScreen> createState() => _ClaimsScreenState();
}

class _ClaimsScreenState extends State<ClaimsScreen> {
  Map<String, dynamic>? pack;
  String error = '';
  bool busy = false;

  Future<void> _open() async {
    setState(() { busy = true; error = ''; });
    try {
      final r = await widget.api.claimsOpen(widget.rsaId);
      setState(() => pack = r);
    } catch (e) { setState(() => error = '$e'); }
    setState(() => busy = false);
  }

  Future<void> _complete(String item) async {
    try {
      final r = await widget.api.claimsComplete(widget.rsaId, item);
      setState(() => pack = r);
    } catch (e) { setState(() => error = '$e'); }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Retirement claim')),
      body: pack == null
          ? Center(child: FilledButton(
              onPressed: busy ? null : _open,
              child: const Text('Start my claim pack')))
          : ListView(padding: const EdgeInsets.all(16), children: [
              Text('Completeness: ${pack!['score']}/100',
                  style: const TextStyle(fontSize: 18, fontWeight: FontWeight.bold)),
              const SizedBox(height: 8),
              LinearProgressIndicator(value: (pack!['score'] as int) / 100),
              const SizedBox(height: 12),
              ...((pack!['items'] as List?) ?? []).map((it) {
                final item = it as Map<String, dynamic>;
                return CheckboxListTile(
                  title: Text(item['label'] as String),
                  value: item['done'] as bool,
                  onChanged: (item['done'] as bool) ? null : (_) => _complete(item['label'] as String),
                );
              }),
              if (pack!['stp'] == true)
                const Card(color: Colors.green, child: Padding(
                  padding: EdgeInsets.all(12),
                  child: Text('Complete — eligible for straight-through processing.',
                      style: TextStyle(color: Colors.white)))),
            ]),
    );
  }
}
