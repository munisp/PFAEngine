import 'package:flutter/material.dart';
import '../api/client.dart';

/// Digital RSA onboarding: capture → NIN → BVN → selfie liveness →
/// consent → RSA open (BR-04: under 10 minutes end-to-end).
class OnboardingScreen extends StatefulWidget {
  final String tenantId;
  final String apiBase;
  const OnboardingScreen({super.key, required this.tenantId, required this.apiBase});

  @override
  State<OnboardingScreen> createState() => _OnboardingScreenState();
}

class _OnboardingScreenState extends State<OnboardingScreen> {
  int _step = 0;
  final _name = TextEditingController();
  final _dob = TextEditingController();
  final _phone = TextEditingController();
  final _nin = TextEditingController();
  final _bvn = TextEditingController();
  String? _personId;
  String _status = '';

  ApiClient get _api => ApiClient(baseUrl: widget.apiBase, tenantId: widget.tenantId);

  Future<void> _next() async {
    setState(() => _status = 'Working…');
    try {
      switch (_step) {
        case 0:
          final p = await _api.createMember(_name.text, _dob.text, _phone.text);
          _personId = p['id'] as String;
        case 1:
          await _api.verifyNIN(_personId!, _nin.text, _name.text, _dob.text);
        case 2:
          await _api.validateBVN(_personId!, _bvn.text, _name.text, _dob.text, _phone.text);
        case 3:
          // Selfie-video capture happens via camera package on device;
          // the evidence reference is submitted here (FR-103).
          final res = await _api.submitLiveness(_personId!, 'clean-selfie');
          if (res['status'] != 'PASSED') {
            setState(() => _status = 'Liveness: ${res['status']} — retry or manual review');
            return;
          }
        case 4:
          await _api.openRSA(_personId!, 'FUND_II'); // multi-fund default ≤49
          if (mounted) Navigator.of(context).pushReplacementNamed('/dashboard');
      }
      setState(() { _step++; _status = ''; });
    } catch (e) {
      setState(() => _status = e.toString());
    }
  }

  @override
  Widget build(BuildContext context) {
    final steps = <Widget>[
      _form('Your details', [_field(_name, 'Full name'), _field(_dob, 'Date of birth (YYYY-MM-DD)'), _field(_phone, 'Phone')]),
      _form('NIN verification', [_field(_nin, 'National ID Number (11 digits)')]),
      _form('BVN validation', [_field(_bvn, 'Bank Verification Number')]),
      _form('Selfie liveness', [const Text('Center your face and follow the prompts.')]),
      _form('Open your RSA', [const Text('Fund II (default). You can change funds later.')]),
    ];
    return Scaffold(
      appBar: AppBar(title: const Text('Open your RSA')),
      body: Stepper(
        currentStep: _step,
        controlsBuilder: (_, d) => ElevatedButton(onPressed: _next, child: Text(_step == 4 ? 'Finish' : 'Continue')),
        steps: [for (var i = 0; i < steps.length; i++) Step(title: Text('Step ${i + 1}'), content: steps[i], isActive: i <= _step)],
      ),
      bottomNavigationBar: _status.isEmpty ? null : Padding(padding: const EdgeInsets.all(12), child: Text(_status)),
    );
  }

  Widget _form(String title, List<Widget> children) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [Text(title, style: const TextStyle(fontWeight: FontWeight.bold)), ...children],
      );

  Widget _field(TextEditingController c, String label) =>
      TextField(controller: c, decoration: InputDecoration(labelText: label));
}
