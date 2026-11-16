import 'package:flutter/material.dart';
import '../api/client.dart';

/// Support screen: raise complaints/service requests, track case status
/// with SLA state, and rate resolutions (CSAT) — member self-service for
/// the unified case management engine (Tranche 20).
class SupportScreen extends StatefulWidget {
  final ApiClient api;
  final String memberId;
  const SupportScreen({super.key, required this.api, required this.memberId});

  @override
  State<SupportScreen> createState() => _SupportScreenState();
}

class _SupportScreenState extends State<SupportScreen> {
  List<dynamic> cases = [];
  String type = 'COMPLAINT';
  final subject = TextEditingController();
  final description = TextEditingController();
  String message = '';
  String error = '';
  bool busy = false;

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  Future<void> _refresh() async {
    try {
      final r = await widget.api.myCases(widget.memberId);
      setState(() => cases = (r['cases'] as List?) ?? []);
    } catch (_) {/* keep stale list offline-first */}
  }

  Future<void> _raise() async {
    if (subject.text.trim().isEmpty) return;
    setState(() { busy = true; error = ''; message = ''; });
    try {
      final r = await widget.api.raiseCase(
          widget.memberId, type, subject.text.trim(), description.text.trim());
      final c = r['case'] as Map<String, dynamic>;
      setState(() {
        message = r['merged'] == true
            ? 'Added to your existing case ${c['id']}'
            : 'Case opened: ${c['id']}';
        subject.clear();
        description.clear();
      });
      await _refresh();
    } catch (e) {
      setState(() => error = '$e');
    }
    setState(() => busy = false);
  }

  Future<void> _rate(String caseId, int score) async {
    try {
      await widget.api.rateCase(caseId, score);
      await _refresh();
    } catch (e) {
      setState(() => error = '$e');
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Support & complaints')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          const Text('Raise a complaint or request',
              style: TextStyle(fontWeight: FontWeight.bold)),
          const SizedBox(height: 8),
          DropdownButtonFormField<String>(
            initialValue: type,
            decoration: const InputDecoration(labelText: 'Type'),
            items: const [
              DropdownMenuItem(value: 'COMPLAINT', child: Text('Complaint')),
              DropdownMenuItem(value: 'SERVICE_REQUEST', child: Text('Service request')),
              DropdownMenuItem(value: 'GENERAL', child: Text('General enquiry')),
            ],
            onChanged: (v) => setState(() => type = v ?? 'COMPLAINT'),
          ),
          TextField(controller: subject,
              decoration: const InputDecoration(labelText: 'Subject')),
          TextField(controller: description,
              decoration: const InputDecoration(labelText: 'What happened?'),
              maxLines: 3),
          const SizedBox(height: 8),
          FilledButton(
            onPressed: busy ? null : _raise,
            child: const Text('Submit'),
          ),
          if (message.isNotEmpty)
            Padding(padding: const EdgeInsets.only(top: 8),
                child: Text(message, style: const TextStyle(color: Colors.green))),
          if (error.isNotEmpty)
            Padding(padding: const EdgeInsets.only(top: 8),
                child: Text(error, style: const TextStyle(color: Colors.red))),
          const Divider(height: 32),
          const Text('My cases', style: TextStyle(fontWeight: FontWeight.bold)),
          if (cases.isEmpty)
            const Padding(padding: EdgeInsets.only(top: 8),
                child: Text('No cases yet — we are here if you need us.')),
          ...cases.map((c) {
            final m = c as Map<String, dynamic>;
            final terminal = m['status'] == 'RESOLVED' || m['status'] == 'CLOSED';
            return Card(
              child: ListTile(
                title: Text('${m['subject']}'),
                subtitle: Text(
                    '${m['id']} · ${m['type']} · ${m['status']}'
                    '${m['sla_breached'] == true && !terminal ? ' ⚠ SLA' : ''}'
                    '${m['resolution'] != null ? '\n${m['resolution_code']}: ${m['resolution']}' : ''}'),
                trailing: terminal && m['csat'] == null
                    ? PopupMenuButton<int>(
                        icon: const Icon(Icons.star_border),
                        onSelected: (s) => _rate('${m['id']}', s),
                        itemBuilder: (_) => [1, 2, 3, 4, 5]
                            .map((s) => PopupMenuItem(value: s, child: Text('$s ★')))
                            .toList(),
                      )
                    : (m['csat'] != null ? Text('${m['csat']}/5 ★') : null),
              ),
            );
          }),
        ],
      ),
    );
  }
}
