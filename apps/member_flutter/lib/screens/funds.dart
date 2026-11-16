import 'package:flutter/material.dart';
import '../api/client.dart';

/// Funds screen: PenCom multi-fund glidepath status + election.
class FundsScreen extends StatefulWidget {
  final ApiClient api;
  final String memberId;
  const FundsScreen({super.key, required this.api, required this.memberId});

  @override
  State<FundsScreen> createState() => _FundsScreenState();
}

class _FundsScreenState extends State<FundsScreen> {
  Map<String, dynamic>? member;
  List<dynamic> switches = [];
  String error = '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final r = await widget.api.glidepathGet(widget.memberId);
      setState(() {
        member = r['member'] as Map<String, dynamic>?;
        switches = (r['switches'] as List?) ?? [];
      });
    } catch (e) { setState(() => error = 'Not registered for glidepath yet'); }
  }

  Future<void> _elect(String fund) async {
    try {
      await widget.api.glidepathElect(widget.memberId, fund);
      await _load();
    } catch (e) { setState(() => error = '$e'); }
  }

  static const _funds = {
    'I': 'Fund I — aggressive (under 50, opt-in)',
    'II': 'Fund II — default growth (under 50)',
    'III': 'Fund III — pre-retirement (50+)',
    'IV': 'Fund IV — retiree',
  };

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('My fund')),
      body: ListView(padding: const EdgeInsets.all(16), children: [
        if (member != null)
          Card(child: ListTile(
            title: Text('Current: Fund ${member!['fund']}'),
            subtitle: Text('Age ${member!['age_years']}'),
          )),
        const SizedBox(height: 8),
        const Text('Elect a fund:', style: TextStyle(fontWeight: FontWeight.bold)),
        ..._funds.entries.map((e) => Card(child: ListTile(
          title: Text(e.value),
          trailing: member != null && member!['fund'] == e.key
              ? const Icon(Icons.check_circle, color: Colors.green)
              : TextButton(onPressed: () => _elect(e.key), child: const Text('Elect')),
        ))),
        if (switches.isNotEmpty) ...[
          const SizedBox(height: 8),
          const Text('Switch history', style: TextStyle(fontWeight: FontWeight.bold)),
          ...switches.map((s) => ListTile(
              dense: true,
              title: Text('Fund ${s['from']} → Fund ${s['to']}'))),
        ],
        if (error.isNotEmpty) Text(error, style: const TextStyle(color: Colors.red)),
      ]),
    );
  }
}
