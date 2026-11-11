using System;
using System.IO;
using System.IO.Compression;
using System.Linq;
using FlaUI.Core.AutomationElements;
using FlaUI.Core.Definitions;
using FlaUI.Core.Input;
using FlaUI.Core.WindowsAPI;
using Xunit;

namespace SpoolSmithGui.Tests;

/// <summary>
/// A printer set (.zip) opened on the exe -- dropped on it, double-clicked
/// or passed as an argument -- opens one combined review: every printer is
/// previewed, listed with its plan and a tick box, and one button sets up the
/// ticked ones after one confirmation. Nothing here confirms it.
/// </summary>
public sealed class SetTests : IDisposable
{
    private readonly string _directory;
    private readonly AppFixture _fixture;

    public SetTests()
    {
        _directory = Path.Combine(Path.GetTempPath(), "spoolsmith-set-test-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(_directory);
        var example = Path.Combine(AppFixture.FindRepoRoot(), "examples", "intune", "accounting.ssb");
        var set = Path.Combine(_directory, "office.zip");
        using (var zip = ZipFile.Open(set, ZipArchiveMode.Create))
        {
            zip.CreateEntryFromFile(example, "accounting.ssb");
            zip.CreateEntryFromFile(example, "accounting-2.ssb");
        }
        _fixture = new AppFixture(profilesDirectory: null, args: set);
    }

    public void Dispose()
    {
        _fixture.Dispose();
        try { Directory.Delete(_directory, recursive: true); } catch (IOException) { }
    }

    [StaFact]
    public void A_set_argument_previews_every_printer_for_one_confirmation()
    {
        Window? dialog = null;
        FunctionalTests.WaitUntil(() => (dialog = _fixture.MainWindow.ModalWindows
            .FirstOrDefault(w => w.Title == "Printer set")) != null, "The set review did not open.", 30_000);

        // Both printers are previewed; the status line says how many are ready.
        string status = "";
        FunctionalTests.WaitUntil(() => (status = dialog!.FindAllDescendants(cf => cf.ByControlType(ControlType.Text))
            .Select(t => t.Name).FirstOrDefault(n => n.Contains("Nothing has changed yet")) ?? "") != "",
            "The set never finished its preview.", 120_000);
        Assert.Contains("2 printers ready to set up", status);

        var table = FunctionalTests.Find(dialog!, "set-members");
        // walk's table exposes its cells, not ListItem rows, to UIA.
        var ready = table.FindAllDescendants().Where(cell => (cell.Name ?? "").StartsWith("Ready:", StringComparison.Ordinal)).ToArray();
        Assert.Equal(2, ready.Length);

        // The selected printer's plan reads as the sheet's steps, not a transcript.
        // (A label's UIA name is its text, so it is found by what it says.)
        var labels = dialog!.FindAllDescendants(cf => cf.ByControlType(ControlType.Text)).Select(t => t.Name ?? "").ToArray();
        var all = string.Join(" | ", labels);
        Assert.True(labels.Any(n => n.Contains("Create printer \"Example — Accounting Copier\"", StringComparison.Ordinal)), all);
        Assert.True(labels.Any(n => n.StartsWith("Example — Accounting Copier  —  Ready:", StringComparison.Ordinal)), all);
        Assert.DoesNotContain(labels, n => n.Contains("Install plan", StringComparison.Ordinal));

        var primary = PrimaryButton(dialog!);
        Assert.StartsWith("Set up 2 printers", primary.Name);
        Assert.True(primary.IsEnabled);

        // Unticking one narrows what the one confirmation would cover.
        // Space toggles the selected row's tick; retry until the click has
        // selected the row and the keystroke reached the list.
        FunctionalTests.WaitUntil(() =>
        {
            if (PrimaryButton(dialog!).Name.StartsWith("Set up 1 printer", StringComparison.Ordinal)) return true;
            dialog!.SetForeground();
            ready[1].Click();
            System.Threading.Thread.Sleep(300);
            Keyboard.Press(VirtualKeyShort.SPACE);
            System.Threading.Thread.Sleep(500);
            return PrimaryButton(dialog!).Name.StartsWith("Set up 1 printer", StringComparison.Ordinal);
        }, "Unticking a printer did not change the button.", 15_000);

        // Details carries each printer's own plan.
        FunctionalTests.FindButton(dialog!, "Details").Invoke();
        Window? details = null;
        FunctionalTests.WaitUntil(() => (details = _fixture.App.GetAllTopLevelWindows(_fixture.Automation)
            .Concat(dialog!.ModalWindows).FirstOrDefault(w => w.Title == "Printer set details")) != null, "Details did not open.");
        // A multi-line read-only text box may report itself as Document.
        var box = details!.FindFirstDescendant(cf => cf.ByControlType(ControlType.Edit).Or(cf.ByControlType(ControlType.Document)));
        Assert.NotNull(box);
        var text = box!.Patterns.Text.IsSupported ? box.Patterns.Text.Pattern.DocumentRange.GetText(-1) : box.AsTextBox().Text;
        Assert.Contains("1 of 2  Example — Accounting Copier", text);
        Assert.Contains("2 of 2  Example — Accounting Copier", text);
        Assert.Contains("Full plan:", text);
        Assert.Contains("RAW9100-192.0.2.40", text);
        FunctionalTests.FindButton(details!, "Close").Invoke();

        FunctionalTests.FindButton(dialog!, "Close").Invoke();
        FunctionalTests.WaitUntil(() => _fixture.MainWindow.IsEnabled, "The set review did not close.");
    }

    private static AutomationElement PrimaryButton(Window dialog) =>
        dialog.FindAllDescendants(cf => cf.ByControlType(ControlType.Button))
            .First(b => b.Name.StartsWith("Set up", StringComparison.Ordinal));
}
